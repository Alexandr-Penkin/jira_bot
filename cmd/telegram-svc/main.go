// Command telegram-svc owns the Telegram-facing slice of the
// SleepJiraBot DDD split.
//
// Phase 6a — NotifyRequested consumer. Producers (bot, poller,
// scheduler, webhook) set NOTIFY_VIA_EVENTS=true to route their Send
// calls through the event bus instead of calling tgbotapi directly.
// telegram-svc is the only subscriber of sjb.notify.requested.v1 —
// ack on success, nak-with-delay on Telegram API errors (at most
// ~5 deliveries before the message is dropped into JetStream's
// terminal state).
//
// Phase 6b — opt-in update handling. When TELEGRAM_SVC_UPDATES=true,
// the service additionally constructs the full dependency graph
// (Mongo repos, OAuth, Jira client, preferences provider, webhook
// manager) and runs `telegram.Bot.Start(ctx)` — the same long-poll
// Handler code the monolith uses, just hosted here. Only one process
// may call getUpdates at a time, so the monolith must be started with
// EMBED_TELEGRAM_UPDATES=false when this flag is on.
//
// Known limitation in Phase 6b: the OAuth multi-site selection flow
// relies on an in-memory pending-site map inside the monolith's
// CallbackServer. When updates run here but the OAuth callback still
// runs on cmd/bot, clicking the site-selection button reaches this
// process's Handler with a nil callbackServer and degrades to a
// generic error. Single-site OAuth is unaffected.
package main

import (
	"context"
	"encoding/hex"
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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"

	"SleepJiraBot/internal/config"
	"SleepJiraBot/internal/crypto"
	"SleepJiraBot/internal/identity"
	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/logger"
	"SleepJiraBot/internal/preferences"
	"SleepJiraBot/internal/proxy"
	"SleepJiraBot/internal/storage"
	"SleepJiraBot/internal/telegram"
	eventsv1 "SleepJiraBot/pkg/events/v1"
	"SleepJiraBot/pkg/identityclient"
	"SleepJiraBot/pkg/natsx"
	"SleepJiraBot/pkg/preferencesclient"
	"SleepJiraBot/pkg/telemetry"
)

var (
	tracer = otel.Tracer("SleepJiraBot/cmd/telegram-svc")
	meter  = otel.Meter("SleepJiraBot/cmd/telegram-svc")

	deliveryCount    metric.Int64Counter
	deliveryDuration metric.Float64Histogram
)

func init() {
	deliveryCount, _ = meter.Int64Counter(
		"sjb.notify.delivered",
		metric.WithDescription("Count of Telegram delivery attempts, labelled by outcome"),
	)
	deliveryDuration, _ = meter.Float64Histogram(
		"sjb.notify.delivery.duration",
		metric.WithDescription("Duration of Telegram send calls"),
		metric.WithUnit("ms"),
	)
}

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

	// Phase 6b: opt-in update handling. Constructs the full dependency
	// graph the monolith builds for its Handler and starts the same
	// long-poll receive loop in this process. Only enabled when
	// TELEGRAM_SVC_UPDATES=true; the monolith must be run with
	// EMBED_TELEGRAM_UPDATES=false so updates reach a single consumer.
	if cfg.TelegramSvcUpdates {
		bot, cleanup, err := startUpdateHandler(ctx, cfg, tgAPI, httpClient, jsPub, log)
		if err != nil {
			log.Error().Err(err).Msg("failed to start telegram-svc update handler")
			cancel()
		} else {
			defer cleanup()
			wg.Add(1)
			go func() {
				defer wg.Done()
				bot.Start(ctx)
			}()
		}
	}

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
	ctx = natsx.ExtractContext(ctx, msg)
	ctx, span := tracer.Start(ctx, "telegram-svc.deliver "+msg.Subject,
		natsx.ConsumerAttrs(msg.Subject)...,
	)
	defer span.End()

	outcome := natsx.OutcomeAck
	consumeStart := time.Now()
	defer func() { natsx.ObserveConsume(ctx, msg.Subject, &outcome, consumeStart) }()

	var env eventsv1.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "malformed envelope")
		log.Error().Err(err).Msg("telegram-svc: malformed envelope; terminating message")
		outcome = natsx.OutcomeTerm
		_ = msg.Term()
		return
	}
	var req eventsv1.NotifyRequested
	if err := json.Unmarshal(env.Payload, &req); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "malformed payload")
		log.Error().Err(err).Msg("telegram-svc: malformed NotifyRequested payload; terminating message")
		outcome = natsx.OutcomeTerm
		_ = msg.Term()
		return
	}
	span.SetAttributes(semconv.MessagingMessageID(env.ID))
	if req.ChatID == 0 || req.Text == "" {
		span.SetStatus(codes.Error, "empty notification")
		log.Warn().Int64("chat_id", req.ChatID).Msg("telegram-svc: rejecting empty notification")
		outcome = natsx.OutcomeTerm
		_ = msg.Term()
		return
	}

	tg := tgbotapi.NewMessage(req.ChatID, req.Text)
	if req.ParseMode != "" {
		tg.ParseMode = req.ParseMode
	}
	tg.DisableWebPagePreview = req.DisableWebPagePreview

	sendStart := time.Now()
	sent, err := tgAPI.Send(tg)
	deliveryDuration.Record(ctx, float64(time.Since(sendStart).Microseconds())/1000.0)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "telegram send failed")
		deliveryCount.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", "failed")))
		log.Error().Err(err).Int64("chat_id", req.ChatID).Str("reason", req.Reason).Msg("telegram-svc: Telegram send failed; will retry")
		_ = pub.Publish(ctx, eventsv1.NotifyFailed{
			ChatID:     req.ChatID,
			TelegramID: req.TelegramID,
			DedupKey:   req.DedupKey,
			Reason:     err.Error(),
			Retryable:  true,
			FailedAt:   time.Now().UnixMilli(),
		}, env.TraceID)
		outcome = natsx.OutcomeNak
		_ = msg.NakWithDelay(nakBackoff)
		return
	}

	deliveryCount.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", "delivered")))
	_ = pub.Publish(ctx, eventsv1.NotifyDelivered{
		ChatID:        req.ChatID,
		TelegramID:    req.TelegramID,
		DedupKey:      req.DedupKey,
		TelegramMsgID: int64(sent.MessageID),
		DeliveredAt:   time.Now().UnixMilli(),
	}, env.TraceID)
	_ = msg.Ack()
}

// startUpdateHandler builds the full dependency graph needed by
// `internal/telegram` handlers and returns a ready-to-Start Bot plus a
// cleanup hook (Mongo disconnect). Mirrors the monolith's wiring in
// cmd/bot/main.go so Phase 6b is behavior-equivalent — only the
// hosting process changes.
func startUpdateHandler(
	ctx context.Context,
	cfg *config.Config,
	tgAPI *tgbotapi.BotAPI,
	httpClient *http.Client,
	eventPub eventsv1.Publisher,
	log zerolog.Logger,
) (*telegram.Bot, func(), error) {
	_ = tgAPI // handler builds its own *tgbotapi.BotAPI via telegram.NewBot

	mongoClient, err := storage.ConnectMongo(ctx, cfg.MongoURI, cfg.MongoDB, log)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		disconnectCtx, disconnectCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer disconnectCancel()
		_ = mongoClient.Disconnect(disconnectCtx)
	}

	if cfg.EncryptionKey == "" || len(cfg.EncryptionKey) != 64 {
		cleanup()
		return nil, nil, errors.New("ENCRYPTION_KEY must be 64 hex characters (32 bytes) for telegram-svc update handling")
	}
	encKeyBytes, err := hex.DecodeString(cfg.EncryptionKey)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	enc, err := crypto.NewEncryptor(encKeyBytes)
	if err != nil {
		cleanup()
		return nil, nil, err
	}

	userRepo := storage.NewUserRepo(mongoClient.Database(), enc)
	subRepo := storage.NewSubscriptionRepo(mongoClient.Database())
	scheduleRepo := storage.NewScheduleRepo(mongoClient.Database())
	webhookRepo := storage.NewWebhookRepo(mongoClient.Database())
	templateRepo := storage.NewTemplateRepo(mongoClient.Database())

	subRepo.SetEventPublisher(eventPub)
	userRepo.SetEventPublisher(eventPub)

	jira.SetHTTPClient(httpClient)
	oauthCfg := jira.OAuthConfig{
		ClientID:     cfg.JiraClientID,
		ClientSecret: cfg.JiraClientSecret,
		RedirectURI:  cfg.JiraRedirectURI,
	}
	oauthClient := jira.NewOAuthClient(oauthCfg, log)
	oauthClient.StartCleanup(ctx)
	jiraClient := jira.NewClient(oauthClient, userRepo, log)
	jiraClient.SetEventPublisher(eventPub)
	jiraClient.StartCleanup(ctx)

	// Route token refresh through identity-svc when IDENTITY_SVC_URL is
	// set so there is a single refresh owner across the fleet. Fall
	// back to an in-process LocalProvider for single-process dev.
	var tokenProvider jira.TokenProvider
	if cfg.IdentitySvcURL != "" {
		remote, err := identityclient.New(cfg.IdentitySvcURL, cfg.InternalAuthToken, nil)
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		tokenProvider = remote
		log.Info().Str("url", cfg.IdentitySvcURL).Msg("telegram-svc: using remote identity-svc for token lease")
	} else {
		local := identity.NewLocalProvider(userRepo, oauthClient, log)
		local.SetEventPublisher(eventPub)
		tokenProvider = local
	}
	jiraClient.SetTokenProvider(tokenProvider)

	webhookMgr := jira.NewWebhookManager(jiraClient, userRepo, webhookRepo, log)

	var prefsProvider preferences.Provider
	if cfg.PreferencesSvcURL != "" && !cfg.EmbedPreferences {
		remote, err := preferencesclient.New(cfg.PreferencesSvcURL, cfg.InternalAuthToken, nil)
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		prefsProvider = remote
		log.Info().Str("url", cfg.PreferencesSvcURL).Msg("telegram-svc: routing preferences through remote preferences-svc")
	} else {
		prefsProvider = preferences.NewLocalProvider(userRepo, log)
	}

	bot, err := telegram.NewBot(
		cfg.TelegramToken,
		oauthClient,
		jiraClient,
		userRepo,
		prefsProvider,
		subRepo,
		scheduleRepo,
		webhookMgr,
		templateRepo,
		log,
		cfg.AdminTelegramID,
		httpClient,
	)
	if err != nil {
		cleanup()
		return nil, nil, err
	}

	if cfg.PersistConversationStates {
		if err := bot.UseMongoStateStore(ctx, mongoClient.Database(), log); err != nil {
			log.Error().Err(err).Msg("telegram-svc: failed to enable Mongo FSM store; falling back to in-memory")
		} else {
			log.Info().Msg("telegram-svc: FSM persisted to Mongo (conversation_states)")
		}
	}

	log.Info().Msg("telegram-svc: update handler wired; starting long-poll loop")
	return bot, cleanup, nil
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
