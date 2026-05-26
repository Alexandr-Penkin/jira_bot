// Command scheduler-svc owns the cron-triggered report scheduler of the
// SleepJiraBot microservice fleet. It shares Mongo and NATS with the rest
// of the fleet and owns:
//   - loading all ScheduledReport documents on startup
//   - firing each report on its cron expression
//   - executing the JQL via a Jira token leased from identity-svc and
//     publishing the result as a NotifyRequested event (telegram-svc
//     delivers)
//
// This slice does NOT listen for schedule-CRUD events yet; a future
// iteration will subscribe to sjb.schedule.{created,updated,deleted}
// events for live config updates across instances.
package main

import (
	"context"
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
	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/logger"
	"SleepJiraBot/internal/proxy"
	"SleepJiraBot/internal/scheduler"
	"SleepJiraBot/internal/storage"
	"SleepJiraBot/pkg/health"
	"SleepJiraBot/pkg/identityclient"
	"SleepJiraBot/pkg/natsx"
	"SleepJiraBot/pkg/notifier"
	"SleepJiraBot/pkg/telemetry"
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

	otelShutdown, err := telemetry.Init(ctx, telemetry.Config{
		Service:  "sjb-scheduler-svc",
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

	enc, err := crypto.NewEncryptorFromHex(cfg.EncryptionKey, cfg.EncryptionKeyPrevious)
	if err != nil {
		log.Error().Err(err).Msg("failed to create encryptor")
		return
	}

	userRepo := storage.NewUserRepo(mongo.Database(), enc)
	scheduleRepo := storage.NewScheduleRepo(mongo.Database())

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
	log.Info().Str("nats_url", cfg.NatsURL).Msg("connected to NATS JetStream")
	eventPub := jsPub
	natsPub := jsPub
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

	if cfg.IdentitySvcURL == "" {
		log.Error().Msg("IDENTITY_SVC_URL is required: Jira tokens are leased from identity-svc")
		return
	}
	tokenProvider, err := identityclient.New(cfg.IdentitySvcURL, cfg.InternalAuthToken, nil)
	if err != nil {
		log.Error().Err(err).Str("url", cfg.IdentitySvcURL).Msg("invalid IDENTITY_SVC_URL")
		return
	}
	jiraClient.SetTokenProvider(tokenProvider)
	log.Info().Str("url", cfg.IdentitySvcURL).Msg("leasing Jira tokens from identity-svc")

	sendNotifier := notifier.NewEvent(eventPub, log)
	log.Info().Msg("notifier: publishing NotifyRequested events; telegram-svc delivers")

	sched := scheduler.New(scheduleRepo, userRepo, jiraClient, sendNotifier, log)
	sched.SetEventPublisher(eventPub)

	healthSrv := &http.Server{
		Addr:              getEnv("SCHEDULER_SVC_ADDR", ":8083"),
		Handler:           health.Mux(buildReadinessProbes(mongo, natsPub)...),
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

func buildReadinessProbes(mongo *storage.MongoDB, natsPub *natsx.JetStreamPublisher) []health.Probe {
	probes := []health.Probe{{
		Name:  "mongo",
		Check: mongo.Ping,
	}}
	if natsPub != nil {
		probes = append(probes, health.Probe{
			Name:  "nats",
			Check: func(_ context.Context) error { return natsPub.Healthy() },
		})
	}
	return probes
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
