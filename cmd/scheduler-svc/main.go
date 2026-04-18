// Command scheduler-svc is the Phase-4 extraction of the cron-triggered
// report scheduler from the SleepJiraBot monolith. It shares Mongo (and
// optionally NATS) with the bot process and owns:
//   - loading all ScheduledReport documents on startup
//   - firing each report on its cron expression
//   - executing the JQL via a leased Jira token and delivering the
//     result to Telegram
//
// The monolith keeps its own embedded scheduler until
// EMBED_SCHEDULER=false, so running scheduler-svc is additive.
//
// This slice does NOT listen for schedule-CRUD events yet; bots reload
// the scheduler in-process when the user edits schedules. A future
// iteration will subscribe to sjb.schedule.{created,updated,deleted}
// events for live config updates across instances.
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
	"SleepJiraBot/internal/proxy"
	"SleepJiraBot/internal/scheduler"
	"SleepJiraBot/internal/storage"
	eventsv1 "SleepJiraBot/pkg/events/v1"
	"SleepJiraBot/pkg/identityclient"
	"SleepJiraBot/pkg/natsx"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func main() {
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		panic("failed to load config: " + err.Error())
	}

	log := logger.New(cfg.LogLevel).With().Str("svc", "scheduler-svc").Logger()
	log.Info().Msg("starting scheduler-svc")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Info().Str("signal", sig.String()).Msg("received shutdown signal")
		cancel()
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
	scheduleRepo := storage.NewScheduleRepo(mongo.Database())

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

	tgAPI, err := tgbotapi.NewBotAPIWithClient(cfg.TelegramToken, tgbotapi.APIEndpoint, httpClient)
	if err != nil {
		log.Error().Err(err).Msg("scheduler-svc: Telegram API init failed")
		return
	}

	sched := scheduler.New(scheduleRepo, userRepo, jiraClient, tgAPI, log)
	sched.SetEventPublisher(eventPub)

	healthSrv := &http.Server{
		Addr:              getEnv("SCHEDULER_SVC_ADDR", ":8083"),
		Handler:           healthHandler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := sched.Start(ctx); err != nil {
			log.Error().Err(err).Msg("scheduler error")
			cancel()
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Info().Str("addr", healthSrv.Addr).Msg("scheduler-svc health server listening")
		if err := healthSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Warn().Err(err).Msg("scheduler-svc health server stopped")
		}
	}()

	<-ctx.Done()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = healthSrv.Shutdown(shutdownCtx)

	wg.Wait()
	log.Info().Msg("scheduler-svc stopped")
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
