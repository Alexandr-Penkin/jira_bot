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
	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/logger"
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

	oauthCfg := jira.OAuthConfig{
		ClientID:     cfg.JiraClientID,
		ClientSecret: cfg.JiraClientSecret,
		RedirectURI:  cfg.JiraRedirectURI,
	}
	oauthClient := jira.NewOAuthClient(oauthCfg, log)
	oauthClient.StartCleanup(ctx)
	jiraClient := jira.NewClient(oauthClient, userRepo, log)
	jiraClient.StartCleanup(ctx)

	bot, err := telegram.NewBot(cfg.TelegramToken, oauthClient, jiraClient, userRepo, subRepo, scheduleRepo, log)
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
		log.Warn().Str("value", cfg.PollInterval).Msg("invalid POLL_INTERVAL, using default 2m")
		pollInterval = 2 * time.Minute
	}
	issuePoller := poller.New(subRepo, userRepo, jiraClient, bot.API(), log, pollInterval)

	webhookHandler := webhook.NewHandler(subRepo, userRepo, bot.API(), cfg.JiraWebhookSecret, log)

	callbackServer := jira.NewCallbackServer(ctx, cfg.CallbackAddr, oauthClient, userRepo, bot.API(), log)
	callbackServer.Handle("/webhook", webhookHandler)
	callbackServer.HandleFunc("/logo.jpeg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(web.LogoJPEG())
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
