// Command telegram-svc is the Phase-6a slice of the SleepJiraBot DDD
// split. It owns a single, narrow concern: consume NotifyRequested
// events from JetStream and deliver them to Telegram.
//
// Producers (bot, poller, scheduler, webhook) set NOTIFY_VIA_EVENTS=true
// to route their Send calls through the event bus instead of calling
// tgbotapi directly. telegram-svc is the only subscriber of
// sjb.notify.requested.v1 — it ack's on successful send, nak's with a
// short backoff on Telegram API errors (at most 5 deliveries before the
// message is dropped into JetStream's terminal state).
//
// Handler-side concerns (wizards, FSM, commands) stay in cmd/bot for
// now. Phase 6b will move the update-handling path here too.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"

	"SleepJiraBot/internal/config"
	"SleepJiraBot/internal/logger"
	"SleepJiraBot/internal/proxy"
	eventsv1 "SleepJiraBot/pkg/events/v1"
	"SleepJiraBot/pkg/natsx"
	"SleepJiraBot/pkg/telemetry"
)

const (
	fetchBatch   = 10
	fetchTimeout = 5 * time.Second
	nakBackoff   = 10 * time.Second
	durableName  = "telegram-svc-notify"
)

func main() {
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		panic("failed to load config: " + err.Error())
	}

	log := logger.New(cfg.LogLevel).With().Str("svc", "telegram-svc").Logger()
	log.Info().Msg("starting telegram-svc")

	if !cfg.EnableEventPublish {
		log.Error().Msg("telegram-svc requires ENABLE_EVENT_PUBLISH=true to consume NotifyRequested events")
		return
	}
	if cfg.TelegramToken == "" {
		log.Error().Msg("TELEGRAM_TOKEN is required")
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Info().Str("signal", sig.String()).Msg("received shutdown signal")
		cancel()
	}()

	otelShutdown, err := telemetry.Init(ctx, telemetry.Config{
		Service:  "sjb-telegram-svc",
		Override: cfg.OtelServiceName,
		Endpoint: cfg.OtelExporterEndpoint,
		Insecure: cfg.OtelExporterInsecure,
	}, log)
	if err != nil {
		log.Error().Err(err).Msg("failed to init OpenTelemetry")
		return
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := otelShutdown(shutdownCtx); err != nil {
			log.Warn().Err(err).Msg("OpenTelemetry shutdown error")
		}
	}()

	httpClient, err := proxy.NewHTTPClient(cfg.ProxyURL, 90*time.Second)
	if err != nil {
		log.Error().Err(err).Msg("failed to create HTTP client")
		return
	}

	tgAPI, err := tgbotapi.NewBotAPIWithClient(cfg.TelegramToken, tgbotapi.APIEndpoint, httpClient)
	if err != nil {
		log.Error().Err(err).Msg("Telegram API init failed")
		return
	}
	log.Info().Str("bot", tgAPI.Self.UserName).Msg("authorized on Telegram")

	jsPub, err := natsx.Connect(ctx, cfg.NatsURL, log)
	if err != nil {
		log.Error().Err(err).Str("nats_url", cfg.NatsURL).Msg("failed to connect to NATS")
		return
	}
	if err := jsPub.EnsureStreams(natsx.DefaultStreams()); err != nil {
		log.Error().Err(err).Msg("failed to ensure JetStream streams")
		_ = jsPub.Close()
		return
	}
	defer func() { _ = jsPub.Close() }()

	sub, err := jsPub.PullSubscribe(eventsv1.SubjectNotifyRequested, durableName)
	if err != nil {
		log.Error().Err(err).Msg("failed to subscribe to notify.requested")
		return
	}
	defer func() { _ = sub.Unsubscribe() }()
	log.Info().Str("subject", eventsv1.SubjectNotifyRequested).Str("durable", durableName).Msg("consuming notify.requested events")

	healthSrv := &http.Server{
		Addr:              getEnv("TELEGRAM_SVC_ADDR", ":8084"),
		Handler:           healthHandler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		runConsumer(ctx, sub, tgAPI, jsPub, log)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Info().Str("addr", healthSrv.Addr).Msg("telegram-svc health server listening")
		if err := healthSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Warn().Err(err).Msg("telegram-svc health server stopped")
		}
	}()

	<-ctx.Done()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = healthSrv.Shutdown(shutdownCtx)

	wg.Wait()
	log.Info().Msg("telegram-svc stopped")
}

func runConsumer(ctx context.Context, sub *nats.Subscription, tgAPI *tgbotapi.BotAPI, pub eventsv1.Publisher, log zerolog.Logger) {
	for {
		if ctx.Err() != nil {
			return
		}
		msgs, err := sub.Fetch(fetchBatch, nats.MaxWait(fetchTimeout))
		if err != nil {
			if errors.Is(err, nats.ErrTimeout) || errors.Is(err, context.Canceled) {
				continue
			}
			log.Warn().Err(err).Msg("telegram-svc: fetch failed")
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
				return
			}
			continue
		}
		for _, msg := range msgs {
			handleMessage(ctx, msg, tgAPI, pub, log)
		}
	}
}

func handleMessage(ctx context.Context, msg *nats.Msg, tgAPI *tgbotapi.BotAPI, pub eventsv1.Publisher, log zerolog.Logger) {
	var env eventsv1.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		log.Error().Err(err).Msg("telegram-svc: malformed envelope; terminating message")
		_ = msg.Term()
		return
	}
	var req eventsv1.NotifyRequested
	if err := json.Unmarshal(env.Payload, &req); err != nil {
		log.Error().Err(err).Msg("telegram-svc: malformed NotifyRequested payload; terminating message")
		_ = msg.Term()
		return
	}
	if req.ChatID == 0 || req.Text == "" {
		log.Warn().Int64("chat_id", req.ChatID).Msg("telegram-svc: rejecting empty notification")
		_ = msg.Term()
		return
	}

	tg := tgbotapi.NewMessage(req.ChatID, req.Text)
	if req.ParseMode != "" {
		tg.ParseMode = req.ParseMode
	}
	tg.DisableWebPagePreview = req.DisableWebPagePreview

	sent, err := tgAPI.Send(tg)
	if err != nil {
		log.Error().Err(err).Int64("chat_id", req.ChatID).Str("reason", req.Reason).Msg("telegram-svc: Telegram send failed; will retry")
		_ = pub.Publish(ctx, eventsv1.NotifyFailed{
			ChatID:     req.ChatID,
			TelegramID: req.TelegramID,
			DedupKey:   req.DedupKey,
			Reason:     err.Error(),
			Retryable:  true,
			FailedAt:   time.Now().UnixMilli(),
		}, env.TraceID)
		_ = msg.NakWithDelay(nakBackoff)
		return
	}

	_ = pub.Publish(ctx, eventsv1.NotifyDelivered{
		ChatID:        req.ChatID,
		TelegramID:    req.TelegramID,
		DedupKey:      req.DedupKey,
		TelegramMsgID: int64(sent.MessageID),
		DeliveredAt:   time.Now().UnixMilli(),
	}, env.TraceID)
	_ = msg.Ack()
}

func healthHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
