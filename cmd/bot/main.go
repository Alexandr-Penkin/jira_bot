// Command bot is the auth + web + calendar service of the SleepJiraBot
// microservice fleet. After the monolith cutover it owns only the
// concerns that have no other home:
//   - the OAuth 2.0 /callback endpoint (token exchange, single-site
//     finalize, multi-site staging) plus the post-auth Telegram UX and
//     webhook registration
//   - the public web pages (landing, /privacy, /logo.jpeg, /health)
//   - the calendar feed poller (reminders fan out as NotifyRequested
//     events; telegram-svc delivers them)
//
// Everything else runs in its own service: token custody (identity-svc),
// preferences (preferences-svc), Jira polling (subscription-svc), cron
// reports (scheduler-svc), webhook ingress (webhook-svc), and Telegram
// update-handling + delivery (telegram-svc). NATS is mandatory — there is
// no in-process fallback — and Jira tokens are leased from identity-svc.
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

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"

	"SleepJiraBot/internal/calendar"
	"SleepJiraBot/internal/calendarpoller"
	"SleepJiraBot/internal/config"
	"SleepJiraBot/internal/crypto"
	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/logger"
	"SleepJiraBot/internal/notifydedup"
	"SleepJiraBot/internal/proxy"
	"SleepJiraBot/internal/storage"
	"SleepJiraBot/pkg/identityclient"
	"SleepJiraBot/pkg/natsx"
	"SleepJiraBot/pkg/notifier"
	"SleepJiraBot/pkg/telemetry"
	"SleepJiraBot/web"
)

func main() {
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		panic("failed to load config: " + err.Error())
	}

	log := logger.New(cfg.LogLevel).With().Str("svc", "bot").Logger()
	log.Info().Msg("starting SleepJiraBot auth/web/calendar service")

	if cfg.IdentitySvcURL == "" {
		log.Error().Msg("IDENTITY_SVC_URL is required: Jira tokens are leased from identity-svc")
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
		Service:  "sjb-bot",
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
	if cfg.EncryptionKeyPrevious != "" {
		log.Info().Msg("crypto: ENCRYPTION_KEY_PREVIOUS registered as fallback for legacy ciphertexts")
	}

	userRepo := storage.NewUserRepo(mongo.Database(), enc)
	subRepo := storage.NewSubscriptionRepo(mongo.Database())
	webhookRepo := storage.NewWebhookRepo(mongo.Database())
	calendarEventRepo := storage.NewCalendarEventRepo(mongo.Database())
	oauthStateRepo := storage.NewOAuthStateRepo(mongo.Database())
	oauthPendingRepo := storage.NewOAuthPendingRepo(mongo.Database(), enc)
	if err := oauthStateRepo.EnsureIndexes(ctx); err != nil {
		log.Warn().Err(err).Msg("failed to ensure oauth_states TTL index; continuing")
	}
	if err := oauthPendingRepo.EnsureIndexes(ctx); err != nil {
		log.Warn().Err(err).Msg("failed to ensure oauth_pending_sites TTL index; continuing")
	}
	if err := calendarEventRepo.EnsureIndexes(ctx); err != nil {
		log.Warn().Err(err).Msg("failed to ensure calendar_events indexes; continuing")
	}

	// NATS is mandatory: calendar reminders fan out as NotifyRequested
	// events and UserAuthenticated is published on connect. A connection
	// failure is fatal — there is no in-process delivery fallback.
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
	log.Info().Str("nats_url", cfg.NatsURL).Msg("event publisher connected to NATS JetStream")
	eventPub := jsPub
	subRepo.SetEventPublisher(eventPub)
	userRepo.SetEventPublisher(eventPub)

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
	oauthClient.SetStateStore(oauthStateRepo)
	oauthClient.StartCleanup(ctx)

	jiraClient := jira.NewClient(oauthClient, userRepo, log)
	jiraClient.SetEventPublisher(eventPub)
	jiraClient.StartCleanup(ctx)

	// Jira tokens are leased from identity-svc so it is the single refresh
	// owner across the fleet — the webhook registration that fires on
	// connect goes through this path.
	tokenProvider, err := identityclient.New(cfg.IdentitySvcURL, cfg.InternalAuthToken, nil)
	if err != nil {
		log.Error().Err(err).Str("url", cfg.IdentitySvcURL).Msg("invalid IDENTITY_SVC_URL")
		return
	}
	jiraClient.SetTokenProvider(tokenProvider)
	log.Info().Str("url", cfg.IdentitySvcURL).Msg("leasing Jira tokens from identity-svc")

	webhookMgr := jira.NewWebhookManager(jiraClient, userRepo, webhookRepo, cfg.JiraWebhookURL, log)

	// The Telegram client is used only for OAuth-flow messages: the
	// multi-site selection keyboard and the post-connect confirmation.
	tgAPI, err := tgbotapi.NewBotAPIWithClient(cfg.TelegramToken, tgbotapi.APIEndpoint, httpClient)
	if err != nil {
		log.Error().Err(err).Msg("Telegram API init failed")
		return
	}
	log.Info().Str("bot", tgAPI.Self.UserName).Msg("authorized on Telegram")

	siteConnector := jira.NewSiteConnector(oauthClient, userRepo, subRepo, webhookMgr, oauthPendingRepo, tgAPI, log)
	siteConnector.SetEventPublisher(eventPub)

	callbackServer := jira.NewCallbackServer(ctx, cfg.CallbackAddr, oauthClient, siteConnector, tgAPI, log)
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

	// Calendar reminders are delivered as events; telegram-svc is the sole
	// Telegram sender.
	calendarNotifier := notifier.NewEvent(eventPub, log)

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

	calendarFetchTimeout := parseDurationOrDefault(cfg.CalendarFetchTimeout, calendar.DefaultFetchTimeout, "CALENDAR_FETCH_TIMEOUT", log)
	calendarFetcher := calendar.NewFetcher(calendarFetchTimeout, calendar.DefaultMaxBytes)
	calendarInterval := parseDurationOrDefault(cfg.CalendarPollInterval, 5*time.Minute, "CALENDAR_POLL_INTERVAL", log)
	calendarLookahead := parseDurationOrDefault(cfg.CalendarLookahead, 24*time.Hour, "CALENDAR_LOOKAHEAD", log)
	calendarNewHorizon := parseDurationOrDefault(cfg.CalendarNewHorizon, time.Hour, "CALENDAR_NEW_HORIZON", log)
	calPoller := calendarpoller.New(
		userRepo,
		calendarEventRepo,
		calendarFetcher,
		calendarpoller.PackageParser{},
		calendarpoller.PackageExpander{},
		calendarNotifier,
		dedup,
		log,
		calendarInterval,
		calendarLookahead,
		calendarNewHorizon,
		cfg.CalendarDefaultReminderMin,
	)

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		calPoller.Start(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := callbackServer.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("callback server failed")
			cancel()
		}
	}()

	<-ctx.Done()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := callbackServer.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("callback server shutdown error")
	}

	wg.Wait()
	log.Info().Msg("SleepJiraBot stopped")
}

// parseDurationOrDefault is a tiny helper for the calendar-feature env
// knobs whose schema mirrors POLL_INTERVAL — duration strings parsed at
// use site. Logs a warning on bad input so a typo doesn't silently fall
// back to the default.
func parseDurationOrDefault(raw string, def time.Duration, envName string, log zerolog.Logger) time.Duration {
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		log.Warn().Str("env", envName).Str("value", raw).Dur("default", def).Msg("invalid duration; using default")
		return def
	}
	return d
}
