// Command subscription-svc is the Phase-3 extraction of the Jira
// polling loop from the SleepJiraBot monolith. It shares Mongo (and
// optionally NATS) with the bot process and owns:
//   - periodic Jira polling for every stored subscription
//   - detection and merged-change fan-out to Telegram
//   - ChangeDetected event publishing (when NATS is enabled)
//
// The monolith keeps its own embedded poller until EMBED_POLLER=false,
// so running subscription-svc is additive: deploy the container, then
// flip the monolith flag.
//
// The service talks to identity-svc via IDENTITY_SVC_URL for tokens
// (falls back to an in-process LocalProvider when empty) so it never
// needs the OAuth client secret in the remote case.
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

	"SleepJiraBot/internal/config"
	"SleepJiraBot/internal/crypto"
	"SleepJiraBot/internal/identity"
	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/logger"
	"SleepJiraBot/internal/notifydedup"
	"SleepJiraBot/internal/poller"
	"SleepJiraBot/internal/proxy"
	"SleepJiraBot/internal/storage"
	eventsv1 "SleepJiraBot/pkg/events/v1"
	"SleepJiraBot/pkg/identityclient"
	"SleepJiraBot/pkg/natsx"
	"SleepJiraBot/pkg/notifier"
	"SleepJiraBot/pkg/telemetry"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func main() {
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		panic("failed to load config: " + err.Error())
	}

	log := logger.New(cfg.LogLevel).With().Str("svc", "subscription-svc").Logger()
	log.Info().Dur("poll_interval", mustDuration(cfg.PollInterval, 30*time.Second)).Msg("starting subscription-svc")

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
		Service:  "sjb-subscription-svc",
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

	var sendNotifier notifier.Notifier
	if cfg.NotifyViaEvents && cfg.EnableEventPublish {
		sendNotifier = notifier.NewEvent(eventPub, log)
		log.Info().Msg("notifier: publishing NotifyRequested events (external telegram-svc expected)")
	} else {
		tgAPI, err := tgbotapi.NewBotAPIWithClient(cfg.TelegramToken, tgbotapi.APIEndpoint, httpClient)
		if err != nil {
			log.Error().Err(err).Msg("subscription-svc: Telegram API init failed")
			return
		}
		sendNotifier = notifier.NewDirect(tgAPI, log)
	}

	pollInterval := mustDuration(cfg.PollInterval, 30*time.Second)
	batchWindow := mustDuration(cfg.BatchWindow, time.Minute)

	var dedup notifydedup.Allower
	if cfg.DedupRedisURL != "" {
		rg, err := notifydedup.NewRedis(cfg.DedupRedisURL, 3*batchWindow, log)
		if err != nil {
			log.Error().Err(err).Msg("subscription-svc: failed to construct redis dedup")
			return
		}
		if err := rg.Ping(ctx); err != nil {
			log.Error().Err(err).Msg("subscription-svc: redis dedup ping failed")
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

	issuePoller := poller.New(subRepo, userRepo, jiraClient, sendNotifier, log, pollInterval, batchWindow, dedup)
	issuePoller.SetEventPublisher(eventPub)

	healthSrv := &http.Server{
		Addr:              getEnv("SUBSCRIPTION_SVC_ADDR", ":8082"),
		Handler:           healthHandler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		issuePoller.Start(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Info().Str("addr", healthSrv.Addr).Msg("subscription-svc health server listening")
		if err := healthSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Warn().Err(err).Msg("subscription-svc health server stopped")
		}
	}()

	<-ctx.Done()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = healthSrv.Shutdown(shutdownCtx)

	wg.Wait()
	log.Info().Msg("subscription-svc stopped")
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

func mustDuration(s string, fallback time.Duration) time.Duration {
	if s == "" {
		return fallback
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fallback
	}
	return d
}
