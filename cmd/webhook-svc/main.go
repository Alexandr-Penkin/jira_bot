// Command webhook-svc owns Jira webhook ingress for the SleepJiraBot
// microservice fleet. It shares Mongo and NATS with the rest of the fleet
// and owns:
//   - POST /webhook HTTP endpoint (HMAC verify, publish WebhookReceived,
//     fan-out as NotifyRequested events, publish WebhookNormalized)
//   - Jira-side webhook registration refresher (24h)
//
// It is the only binary that serves /webhook, so it validates webhook
// auth itself at startup and leases Jira tokens from identity-svc.
package main

import (
	"context"
	"errors"
	"fmt"
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
	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/logger"
	"SleepJiraBot/internal/notifydedup"
	"SleepJiraBot/internal/proxy"
	"SleepJiraBot/internal/storage"
	"SleepJiraBot/internal/webhook"
	"SleepJiraBot/pkg/health"
	"SleepJiraBot/pkg/identityclient"
	"SleepJiraBot/pkg/natsx"
	"SleepJiraBot/pkg/notifier"
	"SleepJiraBot/pkg/telemetry"
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
	// webhook-svc is always the webhook ingress; require explicit
	// authentication configuration here too, since config.Load only
	// enforces it for the embedded monolith path.
	if err := cfg.ValidateWebhookAuth(); err != nil {
		panic("webhook-svc: " + err.Error())
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

	enc, err := crypto.NewEncryptorFromHex(cfg.EncryptionKey, cfg.EncryptionKeyPrevious)
	if err != nil {
		log.Error().Err(err).Msg("failed to create encryptor")
		return
	}

	userRepo := storage.NewUserRepo(mongo.Database(), enc)
	subRepo := storage.NewSubscriptionRepo(mongo.Database())
	webhookRepo := storage.NewWebhookRepo(mongo.Database())

	// The event bus is mandatory: webhook-svc exists to publish
	// WebhookReceived / WebhookNormalized and to fan out reminders as
	// NotifyRequested events. A NATS connection failure is fatal.
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

	// Jira tokens are leased from identity-svc so it is the single refresh
	// owner across the fleet — webhook-svc never needs the OAuth secret to
	// refresh.
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

	webhookMgr := jira.NewWebhookManager(jiraClient, userRepo, webhookRepo, cfg.JiraWebhookURL, log)

	// webhook-svc fans out notifications as NotifyRequested events;
	// telegram-svc is the sole Telegram sender.
	sendNotifier := notifier.NewEvent(eventPub, log)
	log.Info().Msg("notifier: publishing NotifyRequested events; telegram-svc delivers")

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

	webhookHandler := webhook.NewHandler(subRepo, userRepo, sendNotifier, cfg.JiraWebhookSecret, cfg.AllowUnsignedWebhooks, log, dedup)
	webhookHandler.SetEventPublisher(eventPub)

	mux := http.NewServeMux()
	mux.Handle("/webhook", webhookHandler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/readyz", health.Readiness(buildReadinessProbes(mongo, natsPub)...))
	mux.Handle("/internal/stats", newStatsHandler(webhookHandler, webhookRepo, cfg.InternalAuthToken, log))

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

// newStatsHandler returns an admin-only JSON endpoint with the in-process
// webhook event counter and the persisted webhook-registration count.
// Authenticates with INTERNAL_AUTH_TOKEN (Bearer) when configured; when
// empty, the listener is assumed to be network-protected and any caller
// is allowed — matching the identity-svc lease endpoint policy.
func newStatsHandler(wh *webhook.Handler, repo *storage.WebhookRepo, authToken string, log zerolog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if authToken != "" {
			header := r.Header.Get("Authorization")
			if header != "Bearer "+authToken {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		var webhookCount int64
		if repo != nil {
			n, err := repo.CountAll(r.Context())
			if err != nil {
				log.Warn().Err(err).Msg("internal/stats: failed to count webhook registrations")
			} else {
				webhookCount = n
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"events_received":%d,"webhook_count":%d}`, wh.EventsReceived(), webhookCount)
	})
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
