// Command preferences-svc is the Phase-5 extraction of user-preference
// storage (language, default project/board, JQL filters, field
// mappings, done/hold statuses) from the monolith. It exposes the HTTP
// protocol declared in pkg/preferencesv1 so sibling services
// (subscription-svc, scheduler-svc, webhook-svc, telegram-svc) can
// read and write preferences without carrying the Mongo connection
// for the users collection.
//
// This initial slice shares Mongo with the monolith: both processes
// read and write the same users collection. A future Phase 5b splits
// preference fields into a dedicated user_preferences collection.
//
// Event emission (LanguageChanged, DefaultsChanged) lives inside
// UserRepo setters, so running preferences-svc alongside the monolith
// is safe — whichever process writes is the one that publishes.
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
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"SleepJiraBot/internal/config"
	"SleepJiraBot/internal/crypto"
	"SleepJiraBot/internal/logger"
	"SleepJiraBot/internal/preferences"
	"SleepJiraBot/internal/storage"
	eventsv1 "SleepJiraBot/pkg/events/v1"
	"SleepJiraBot/pkg/natsx"
	"SleepJiraBot/pkg/telemetry"
)

func main() {
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		panic("failed to load config: " + err.Error())
	}

	log := logger.New(cfg.LogLevel).With().Str("svc", "preferences-svc").Logger()
	log.Info().Str("addr", cfg.PreferencesSvcAddr).Msg("starting preferences-svc")

	if cfg.InternalAuthToken == "" {
		log.Warn().Msg("INTERNAL_AUTH_TOKEN is empty; the preferences endpoint is unauthenticated and must be bound to an internal network")
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
		Service:  "sjb-preferences-svc",
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

	provider := preferences.NewLocalProvider(userRepo, log)
	server := preferences.NewServer(provider, cfg.InternalAuthToken, log)

	srv := &http.Server{
		Addr:              cfg.PreferencesSvcAddr,
		Handler:           otelhttp.NewHandler(server.Handler(), "preferences-svc"),
		ReadHeaderTimeout: 10 * time.Second,
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Info().Str("addr", srv.Addr).Msg("preferences-svc HTTP server listening")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("preferences-svc HTTP server failed")
			cancel()
		}
	}()

	<-ctx.Done()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("preferences-svc shutdown error")
	}

	wg.Wait()
	log.Info().Msg("preferences-svc stopped")
}
