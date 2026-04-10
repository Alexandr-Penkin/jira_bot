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
	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/logger"
	"SleepJiraBot/internal/notifydedup"
	"SleepJiraBot/internal/poller"
	"SleepJiraBot/internal/scheduler"
	"SleepJiraBot/internal/storage"
	"SleepJiraBot/internal/telegram"
	"SleepJiraBot/internal/webhook"
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

	oauthCfg := jira.OAuthConfig{
		ClientID:     cfg.JiraClientID,
		ClientSecret: cfg.JiraClientSecret,
		RedirectURI:  cfg.JiraRedirectURI,
	}
	oauthClient := jira.NewOAuthClient(oauthCfg, log)
	oauthClient.StartCleanup(ctx)
	jiraClient := jira.NewClient(oauthClient, userRepo, log)
	jiraClient.StartCleanup(ctx)
	webhookMgr := jira.NewWebhookManager(jiraClient, userRepo, webhookRepo, log)

	bot, err := telegram.NewBot(cfg.TelegramToken, oauthClient, jiraClient, userRepo, subRepo, scheduleRepo, webhookMgr, log, cfg.AdminTelegramID)
	if err != nil {
		log.Error().Err(err).Msg("failed to create telegram bot")
		cancel()
		return
	}

	sched := scheduler.New(scheduleRepo, userRepo, jiraClient, bot.API(), log)

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
	dedup := notifydedup.New(3 * batchWindow)

	issuePoller := poller.New(subRepo, userRepo, jiraClient, bot.API(), log, pollInterval, batchWindow, dedup)
	bot.SetPollerRef(issuePoller)

	webhookHandler := webhook.NewHandler(subRepo, userRepo, bot.API(), cfg.JiraWebhookSecret, log, dedup)

	callbackServer := jira.NewCallbackServer(ctx, cfg.CallbackAddr, oauthClient, userRepo, subRepo, webhookMgr, bot.API(), log)
	callbackServer.Handle("/webhook", webhookHandler)
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

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		issuePoller.Start(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		bot.Start(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := sched.Start(ctx); err != nil {
			log.Error().Err(err).Msg("scheduler error")
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		webhookHandler.Start(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := callbackServer.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("callback server failed")
			cancel()
		}
	}()

	// Webhook refresher: extends Jira-side webhook lifetime before the
	// 30-day expiry. Runs once at startup so a long-restarted instance
	// catches up immediately, then daily.
	wg.Add(1)
	go func() {
		defer wg.Done()
		runWebhookRefresher(ctx, webhookMgr, log)
	}()

	sig := <-sigCh
	log.Info().Str("signal", sig.String()).Msg("received shutdown signal")
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := callbackServer.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("callback server shutdown error")
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
