// Command webhook-svc is the Phase-1 extraction of Jira webhook ingress
// from the SleepJiraBot monolith. It shares Mongo and NATS with the bot
// process and owns:
//   - POST /webhook HTTP endpoint (HMAC verify, publish WebhookReceived,
//     fan-out, publish WebhookNormalized)
//   - Jira-side webhook registration refresher (24h)
//
// The monolith keeps embedded webhook ingress until EMBED_WEBHOOK_SERVER
// is set to false, so running webhook-svc is additive: redirect Jira
// webhook URLs to the webhook-svc address and toggle the flag off.
package main

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"SleepJiraBot/internal/config"
	"SleepJiraBot/internal/crypto"
	"SleepJiraBot/internal/identity"
	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/logger"
	"SleepJiraBot/internal/notifydedup"
	"SleepJiraBot/internal/proxy"
	"SleepJiraBot/internal/storage"
	"SleepJiraBot/internal/webhook"
	eventsv1 "SleepJiraBot/pkg/events/v1"
	"SleepJiraBot/pkg/identityclient"
	"SleepJiraBot/pkg/natsx"
	"SleepJiraBot/pkg/notifier"
	"SleepJiraBot/pkg/telemetry"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	webhookRefreshInterval = 24 * time.Hour
	webhookRefreshLeadTime = 7 * 24 * time.Hour
)

func main() {
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		panic("failed to load config: " + err.Error())
	}

	log := logger.New(cfg.LogLevel).With().Str("svc", "webhook-svc").Logger()
	log.Info().Str("addr", cfg.WebhookSvcAddr).Msg("starting webhook-svc")

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
		Service:  "sjb-webhook-svc",
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

	mongo, err := storage.ConnectMongo(ctx, cfg.MongoURI, cfg.MongoDB, log)
	if err != nil {
		log.Error().Err(err).Msg("failed to connect to MongoDB")
		return
	}
	defer func() {
		disconnectCtx, disconnectCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer disconnectCancel()
		_ = mongo.Disconnect(disconnectCtx)
	}()

	encKeyBytes, err := hex.DecodeString(cfg.EncryptionKey)
	if err != nil {
		log.Error().Err(err).Msg("ENCRYPTION_KEY must be a valid hex string (64 hex chars = 32 bytes)")
		return
	}
	enc, err := crypto.NewEncryptor(encKeyBytes)
	if err != nil {
		log.Error().Err(err).Msg("failed to create encryptor")
		return
	}

	userRepo := storage.NewUserRepo(mongo.Database(), enc)
	subRepo := storage.NewSubscriptionRepo(mongo.Database())
	webhookRepo := storage.NewWebhookRepo(mongo.Database())

	// Event publisher is required for this service: its purpose is to
	// publish WebhookReceived / WebhookNormalized. Missing NATS is fatal.
	if !cfg.EnableEventPublish {
		log.Warn().Msg("ENABLE_EVENT_PUBLISH is false; webhook-svc still runs but downstream consumers will not see events")
	}
	var eventPub eventsv1.Publisher = eventsv1.NoopPublisher{}
	if cfg.EnableEventPublish {
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
		eventPub = jsPub
		log.Info().Str("nats_url", cfg.NatsURL).Msg("connected to NATS JetStream")
		defer func() { _ = jsPub.Close() }()
	}
	subRepo.SetEventPublisher(eventPub)
	userRepo.SetEventPublisher(eventPub)

	httpClient, err := proxy.NewHTTPClient(cfg.ProxyURL, 90*time.Second)
	if err != nil {
		log.Error().Err(err).Msg("failed to create HTTP client")
		return
	}
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

	// Token custody: prefer remote identity-svc when IDENTITY_SVC_URL is
	// set, otherwise run an in-process LocalProvider against the shared
	// Mongo. Both satisfy jira.TokenProvider; the remote path is what
	// Phase-2b points at once identity-svc is deployed.
	var tokenProvider jira.TokenProvider
	if cfg.IdentitySvcURL != "" {
		remote, err := identityclient.New(cfg.IdentitySvcURL, cfg.InternalAuthToken, nil)
		if err != nil {
			log.Error().Err(err).Str("url", cfg.IdentitySvcURL).Msg("invalid IDENTITY_SVC_URL")
			return
		}
		tokenProvider = remote
		log.Info().Str("url", cfg.IdentitySvcURL).Msg("using remote identity-svc for token lease")
	} else {
		local := identity.NewLocalProvider(userRepo, oauthClient, log)
		local.SetEventPublisher(eventPub)
		tokenProvider = local
		log.Info().Msg("using in-process local token provider")
	}
	jiraClient.SetTokenProvider(tokenProvider)

	webhookMgr := jira.NewWebhookManager(jiraClient, userRepo, webhookRepo, log)

	// webhook-svc delivers notifications via the notifier seam: when
	// NOTIFY_VIA_EVENTS is on, it publishes NotifyRequested events and a
	// separate telegram-svc delivers; otherwise it direct-sends through
	// its own tgbotapi client (the same code path the monolith uses when
	// EMBED_WEBHOOK_SERVER=true).
	var sendNotifier notifier.Notifier
	if cfg.NotifyViaEvents && cfg.EnableEventPublish {
		sendNotifier = notifier.NewEvent(eventPub, log)
		log.Info().Msg("notifier: publishing NotifyRequested events (external telegram-svc expected)")
	} else {
		var tgAPI *tgbotapi.BotAPI
		if cfg.TelegramToken != "" {
			tgAPI, err = tgbotapi.NewBotAPIWithClient(cfg.TelegramToken, tgbotapi.APIEndpoint, httpClient)
			if err != nil {
				log.Warn().Err(err).Msg("webhook-svc: Telegram API init failed; direct Telegram sends disabled")
			}
		}
		sendNotifier = notifier.NewDirect(tgAPI, log)
	}

	batchWindow, err := time.ParseDuration(cfg.BatchWindow)
	if err != nil {
		batchWindow = 1 * time.Minute
	}

	var dedup notifydedup.Allower
	if cfg.DedupRedisURL != "" {
		rg, err := notifydedup.NewRedis(cfg.DedupRedisURL, 3*batchWindow, log)
		if err != nil {
			log.Error().Err(err).Msg("webhook-svc: failed to construct redis dedup")
			return
		}
		if err := rg.Ping(ctx); err != nil {
			log.Error().Err(err).Msg("webhook-svc: redis dedup ping failed")
			return
		}
		defer func() { _ = rg.Close() }()
		dedup = rg
		log.Info().Msg("notifydedup: using redis backend")
	} else {
		memDedup := notifydedup.New(3 * batchWindow)
		defer memDedup.Stop()
		dedup = memDedup
	}

	webhookHandler := webhook.NewHandler(subRepo, userRepo, sendNotifier, cfg.JiraWebhookSecret, log, dedup)
	webhookHandler.SetEventPublisher(eventPub)

	mux := http.NewServeMux()
	mux.Handle("/webhook", webhookHandler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:              cfg.WebhookSvcAddr,
		Handler:           otelhttp.NewHandler(mux, "webhook-svc"),
		ReadHeaderTimeout: 10 * time.Second,
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		webhookHandler.Start(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Info().Str("addr", srv.Addr).Msg("webhook HTTP server listening")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("webhook server failed")
			cancel()
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		runWebhookRefresher(ctx, webhookMgr, log)
	}()

	<-ctx.Done()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("webhook server shutdown error")
	}

	wg.Wait()
	log.Info().Msg("webhook-svc stopped")
}

func runWebhookRefresher(ctx context.Context, mgr *jira.WebhookManager, log zerolog.Logger) {
	if mgr == nil {
		return
	}
	log.Info().
		Dur("interval", webhookRefreshInterval).
		Dur("lead_time", webhookRefreshLeadTime).
		Msg("webhook refresher started")

	mgr.RefreshExpiring(ctx, time.Now().Add(webhookRefreshLeadTime))

	ticker := time.NewTicker(webhookRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("webhook refresher stopped")
			return
		case <-ticker.C:
			mgr.RefreshExpiring(ctx, time.Now().Add(webhookRefreshLeadTime))
		}
	}
}
