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

	"SleepJiraBot/internal/config"
	"SleepJiraBot/internal/crypto"
	"SleepJiraBot/internal/identity"
	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/logger"
	"SleepJiraBot/internal/notifydedup"
	"SleepJiraBot/internal/poller"
	"SleepJiraBot/internal/preferences"
	"SleepJiraBot/internal/proxy"
	"SleepJiraBot/internal/scheduler"
	"SleepJiraBot/internal/storage"
	"SleepJiraBot/internal/telegram"
	"SleepJiraBot/internal/webhook"
	eventsv1 "SleepJiraBot/pkg/events/v1"
	"SleepJiraBot/pkg/natsx"
	"SleepJiraBot/pkg/preferencesclient"
	"SleepJiraBot/web"
)

func main() {
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		panic("failed to load config: " + err.Error())
	}

	log := logger.New(cfg.LogLevel)
	log.Info().Msg("starting SleepJiraBot")

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
		cancel()
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
		cancel()
		return
	}
	enc, err := crypto.NewEncryptor(encKeyBytes)
	if err != nil {
		log.Error().Err(err).Msg("failed to create encryptor")
		cancel()
		return
	}

	userRepo := storage.NewUserRepo(mongo.Database(), enc)
	subRepo := storage.NewSubscriptionRepo(mongo.Database())
	scheduleRepo := storage.NewScheduleRepo(mongo.Database())
	webhookRepo := storage.NewWebhookRepo(mongo.Database())
	templateRepo := storage.NewTemplateRepo(mongo.Database())

	// Phase 0 of the DDD microservices split: publish domain events to a
	// NATS JetStream cluster alongside every primary write path. Gated by
	// ENABLE_EVENT_PUBLISH so production can roll it out gradually and fall
	// back by toggling one env var.
	var eventPub eventsv1.Publisher = eventsv1.NoopPublisher{}
	if cfg.EnableEventPublish {
		jsPub, err := natsx.Connect(ctx, cfg.NatsURL, log)
		if err != nil {
			log.Error().Err(err).Str("nats_url", cfg.NatsURL).Msg("failed to connect to NATS; continuing without event publish")
		} else {
			if err := jsPub.EnsureStreams(natsx.DefaultStreams()); err != nil {
				log.Error().Err(err).Msg("failed to ensure JetStream streams; continuing without event publish")
				_ = jsPub.Close()
			} else {
				eventPub = jsPub
				log.Info().Str("nats_url", cfg.NatsURL).Msg("event publisher connected to NATS JetStream")
				defer func() { _ = jsPub.Close() }()
			}
		}
	}
	subRepo.SetEventPublisher(eventPub)
	userRepo.SetEventPublisher(eventPub)

	// Telegram long-polling sets u.Timeout=60s server-side, so the HTTP
	// client timeout must comfortably exceed that (request body read
	// time + network jitter) to avoid aborting healthy long polls.
	httpClient, err := proxy.NewHTTPClient(cfg.ProxyURL, 90*time.Second)
	if err != nil {
		log.Error().Err(err).Msg("failed to create HTTP client with proxy")
		return
	}
	if cfg.ProxyURL != "" {
		log.Info().Str("proxy", cfg.ProxyURL).Msg("using SOCKS proxy for outbound connections")
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
	webhookMgr := jira.NewWebhookManager(jiraClient, userRepo, webhookRepo, log)

	// Phase 2: identity-svc TokenLease. The monolith exposes the
	// protocol on an internal listener so Phase-3 services (webhook-svc,
	// scheduler-svc, subscription-svc) can migrate to remote leases
	// without a second extraction round. LocalProvider shares the same
	// refresh mutex domain as jira.Client so, until Phase 2b switches
	// jira.Client to consume the lease, both paths are protected by
	// their own locks — concurrent refreshes remain impossible at the
	// Mongo write layer because UpdateTokens is atomic, but this is
	// worth retiring in 2b.
	identityProvider := identity.NewLocalProvider(userRepo, oauthClient, log)
	identityProvider.SetEventPublisher(eventPub)

	// Phase 5: construct the preferences provider. When
	// PREFERENCES_SVC_URL is set and EMBED_PREFERENCES=false, the
	// monolith's telegram handlers write preferences through the remote
	// HTTP client; otherwise they go through the in-process
	// LocalProvider against the shared users collection. Either way
	// UserRepo publishes LanguageChanged / DefaultsChanged events, so
	// co-resident operation never double-publishes.
	var prefsProvider preferences.Provider
	if cfg.PreferencesSvcURL != "" && !cfg.EmbedPreferences {
		remote, err := preferencesclient.New(cfg.PreferencesSvcURL, cfg.InternalAuthToken, nil)
		if err != nil {
			log.Error().Err(err).Str("url", cfg.PreferencesSvcURL).Msg("failed to construct preferences client")
			return
		}
		prefsProvider = remote
		log.Info().Str("url", cfg.PreferencesSvcURL).Msg("preferences: routing through remote preferences-svc")
	} else {
		prefsProvider = preferences.NewLocalProvider(userRepo, log)
	}
	// Route jira.Client through the lease provider so the monolith and
	// identity-svc cannot race on refresh-token rotation: the provider
	// serialises refreshes per user, and there is only one provider.
	jiraClient.SetTokenProvider(identityProvider)
	identityServer := identity.NewServer(identityProvider, cfg.InternalAuthToken, log)
	internalSrv := &http.Server{
		Addr:              cfg.InternalAddr,
		Handler:           identityServer.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	if cfg.InternalAuthToken == "" {
		log.Warn().Str("addr", cfg.InternalAddr).Msg("identity: INTERNAL_AUTH_TOKEN empty; lease endpoint relies on network-level protection")
	}

	var bot *telegram.Bot
	for attempt := 1; ; attempt++ {
		bot, err = telegram.NewBot(cfg.TelegramToken, oauthClient, jiraClient, userRepo, prefsProvider, subRepo, scheduleRepo, webhookMgr, templateRepo, log, cfg.AdminTelegramID, httpClient)
		if err == nil {
			break
		}
		delay := time.Duration(attempt) * 5 * time.Second
		if delay > 60*time.Second {
			delay = 60 * time.Second
		}
		log.Warn().Err(err).Dur("retry_in", delay).Int("attempt", attempt).Msg("failed to create telegram bot, retrying")
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			log.Error().Msg("shutdown requested while waiting for Telegram API")
			return
		}
	}

	if cfg.PersistConversationStates {
		if err := bot.UseMongoStateStore(ctx, mongo.Database(), log); err != nil {
			log.Error().Err(err).Msg("failed to enable Mongo FSM store; falling back to in-memory")
		} else {
			log.Info().Msg("telegram FSM persisted to Mongo (conversation_states)")
		}
	}

	sched := scheduler.New(scheduleRepo, userRepo, jiraClient, bot.API(), log)
	sched.SetEventPublisher(eventPub)

	bot.SetOnScheduleChange(func() {
		if err := sched.Reload(context.Background()); err != nil {
			log.Error().Err(err).Msg("failed to reload schedules")
		}
	})

	pollInterval, err := time.ParseDuration(cfg.PollInterval)
	if err != nil {
		log.Warn().Str("value", cfg.PollInterval).Msg("invalid POLL_INTERVAL, using default 30s")
		pollInterval = 30 * time.Second
	}
	batchWindow, err := time.ParseDuration(cfg.BatchWindow)
	if err != nil {
		log.Warn().Str("value", cfg.BatchWindow).Msg("invalid BATCH_WINDOW, using default 1m")
		batchWindow = 1 * time.Minute
	}
	var dedup notifydedup.Allower
	if cfg.DedupRedisURL != "" {
		rg, err := notifydedup.NewRedis(cfg.DedupRedisURL, 3*batchWindow, log)
		if err != nil {
			log.Error().Err(err).Msg("failed to construct redis dedup")
			return
		}
		if err := rg.Ping(ctx); err != nil {
			log.Error().Err(err).Msg("redis dedup ping failed")
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

	issuePoller := poller.New(subRepo, userRepo, jiraClient, bot.API(), log, pollInterval, batchWindow, dedup)
	issuePoller.SetEventPublisher(eventPub)
	bot.SetPollerRef(issuePoller)

	webhookHandler := webhook.NewHandler(subRepo, userRepo, bot.API(), cfg.JiraWebhookSecret, log, dedup)
	webhookHandler.SetEventPublisher(eventPub)
	bot.SetWebhookStats(webhookRepo, webhookHandler.EventsReceived)

	callbackServer := jira.NewCallbackServer(ctx, cfg.CallbackAddr, oauthClient, userRepo, subRepo, webhookMgr, bot.API(), log)
	callbackServer.SetEventPublisher(eventPub)
	if cfg.EmbedWebhookServer {
		callbackServer.Handle("/webhook", webhookHandler)
	} else {
		log.Info().Msg("webhook ingress handled externally; monolith /webhook route disabled")
	}
	callbackServer.HandleFunc("/logo.jpeg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(web.LogoJPEG())
	})
	callbackServer.HandleFunc("/privacy", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(web.PrivacyHTML())
	})
	callbackServer.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(web.LandingHTML())
	})

	bot.SetCallbackServer(callbackServer)

	var wg sync.WaitGroup

	if cfg.EmbedPoller {
		wg.Add(1)
		go func() {
			defer wg.Done()
			issuePoller.Start(ctx)
		}()
	} else {
		log.Info().Msg("polling handled externally; monolith poller disabled")
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		bot.Start(ctx)
	}()

	if cfg.EmbedScheduler {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := sched.Start(ctx); err != nil {
				log.Error().Err(err).Msg("scheduler error")
			}
		}()
	} else {
		log.Info().Msg("scheduling handled externally; monolith scheduler disabled")
	}

	if cfg.EmbedWebhookServer {
		wg.Add(1)
		go func() {
			defer wg.Done()
			webhookHandler.Start(ctx)
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := callbackServer.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("callback server failed")
			cancel()
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Info().Str("addr", internalSrv.Addr).Msg("identity lease server listening")
		if err := internalSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("identity lease server failed")
			cancel()
		}
	}()

	// Webhook refresher: extends Jira-side webhook lifetime before the
	// 30-day expiry. Runs once at startup so a long-restarted instance
	// catches up immediately, then daily. Owned by webhook-svc when
	// EMBED_WEBHOOK_SERVER=false.
	if cfg.EmbedWebhookServer {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runWebhookRefresher(ctx, webhookMgr, log)
		}()
	}

	<-ctx.Done()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := callbackServer.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("callback server shutdown error")
	}
	if err := internalSrv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("identity lease server shutdown error")
	}

	wg.Wait()
	log.Info().Msg("SleepJiraBot stopped")
}

const (
	webhookRefreshInterval = 24 * time.Hour
	// webhookRefreshLeadTime is how far before expiry we extend a
	// webhook. 7 days gives us a generous safety margin so a missed
	// daily run does not let webhooks lapse.
	webhookRefreshLeadTime = 7 * 24 * time.Hour
)

func runWebhookRefresher(ctx context.Context, mgr *jira.WebhookManager, log zerolog.Logger) {
	if mgr == nil {
		return
	}
	log.Info().
		Dur("interval", webhookRefreshInterval).
		Dur("lead_time", webhookRefreshLeadTime).
		Msg("webhook refresher started")

	// Initial run on startup so restarts after long downtime catch up.
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
