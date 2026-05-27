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
	"SleepJiraBot/internal/identity"
	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/logger"
	"SleepJiraBot/internal/proxy"
	"SleepJiraBot/internal/storage"
	"SleepJiraBot/pkg/health"
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

	enc, err := crypto.NewEncryptorFromHex(cfg.EncryptionKey, cfg.EncryptionKeyPrevious)
	if err != nil {
		log.Error().Err(err).Msg("failed to create encryptor")
		return
	}

	userRepo := storage.NewUserRepo(mongo.Database(), enc)

	// identity-svc is the sole token-refresh owner, so TokensRefreshed is
	// emitted from here. The event bus is mandatory; a NATS connection
	// failure is fatal.
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

	// Compose health endpoints onto the lease API mux so /healthz +
	// /readyz live on the same listener as the rest of identity-svc.
	rootMux := http.NewServeMux()
	rootMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	rootMux.Handle("/readyz", health.Readiness(buildReadinessProbes(mongo, natsPub)...))
	rootMux.Handle("/", server.Handler())

	srv := &http.Server{
		Addr:              cfg.InternalAddr,
		Handler:           otelhttp.NewHandler(rootMux, "identity-svc"),
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
