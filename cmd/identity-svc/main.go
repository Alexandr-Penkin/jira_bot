// Command identity-svc is the Phase-2 extraction of Jira token custody
// and refresh from the monolith. It exposes /internal/lease (see
// pkg/identityv1) so sibling services (webhook-svc in Phase 1,
// subscription-svc and scheduler-svc later) can obtain access tokens
// without carrying the OAuth client secret or the encryption key.
//
// This initial slice shares Mongo with the monolith: both processes
// read and write the same users collection. Running both at once is
// safe only because Mongo UpdateTokens is atomic — but two concurrent
// refreshes could still rotate refresh tokens and invalidate each
// other. Until Phase 2b points the monolith's jira.Client at the lease
// endpoint (via IDENTITY_SVC_URL), avoid calling identity-svc from
// services that the monolith is already serving.
//
// The OAuth /callback HTTP route stays in the monolith for now. Moving
// the callback here is Phase 2b: once callback lives in identity-svc,
// the monolith can consume UserAuthenticated events for subscription
// bootstrap instead of running the callback logic itself.
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

	log := logger.New(cfg.LogLevel).With().Str("svc", "identity-svc").Logger()
	log.Info().Str("addr", cfg.InternalAddr).Msg("starting identity-svc")

	if cfg.InternalAuthToken == "" {
		log.Warn().Msg("INTERNAL_AUTH_TOKEN is empty; the lease endpoint is unauthenticated and must be bound to an internal network")
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
		Service:  "sjb-identity-svc",
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

	// Event publisher is optional for identity-svc itself — the monolith
	// publishes UserAuthenticated — but TokensRefreshed should be
	// emitted from whichever process actually refreshes. Enable it by
	// default when NATS is configured.
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

	httpClient, err := proxy.NewHTTPClient(cfg.ProxyURL, 30*time.Second)
	if err != nil {
		log.Error().Err(err).Msg("failed to create HTTP client")
		return
	}
	jira.SetHTTPClient(httpClient)

	oauthClient := jira.NewOAuthClient(jira.OAuthConfig{
		ClientID:     cfg.JiraClientID,
		ClientSecret: cfg.JiraClientSecret,
		RedirectURI:  cfg.JiraRedirectURI,
	}, log)
	oauthClient.StartCleanup(ctx)

	provider := identity.NewLocalProvider(userRepo, oauthClient, log)
	provider.SetEventPublisher(eventPub)
	server := identity.NewServer(provider, cfg.InternalAuthToken, log)

	srv := &http.Server{
		Addr:              cfg.InternalAddr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Info().Str("addr", srv.Addr).Msg("identity-svc HTTP server listening")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("identity-svc HTTP server failed")
			cancel()
		}
	}()

	<-ctx.Done()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("identity-svc shutdown error")
	}

	wg.Wait()
	log.Info().Msg("identity-svc stopped")
}
