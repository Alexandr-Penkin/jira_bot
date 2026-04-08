package config

import (
	"errors"
	"os"
	"strconv"
)

type Config struct {
	TelegramToken     string
	MongoURI          string
	MongoDB           string
	LogLevel          string
	JiraClientID      string
	JiraClientSecret  string
	JiraRedirectURI   string
	PollInterval      string
	BatchWindow       string
	CallbackAddr      string
	EncryptionKey     string
	JiraWebhookSecret string
	AdminTelegramID   int64
}

func Load() (*Config, error) {
	cfg := &Config{
		TelegramToken:     os.Getenv("TELEGRAM_TOKEN"),
		MongoURI:          getEnvOrDefault("MONGO_URI", "mongodb://localhost:27017"),
		MongoDB:           getEnvOrDefault("MONGO_DB", "sleepjirabot"),
		LogLevel:          getEnvOrDefault("LOG_LEVEL", "info"),
		JiraClientID:      os.Getenv("JIRA_CLIENT_ID"),
		JiraClientSecret:  os.Getenv("JIRA_CLIENT_SECRET"),
		JiraRedirectURI:   getEnvOrDefault("JIRA_REDIRECT_URI", "http://localhost:8080/callback"),
		PollInterval:      getEnvOrDefault("POLL_INTERVAL", "30s"),
		BatchWindow:       getEnvOrDefault("BATCH_WINDOW", "1m"),
		CallbackAddr:      getEnvOrDefault("CALLBACK_ADDR", ":8080"),
		EncryptionKey:     os.Getenv("ENCRYPTION_KEY"),
		JiraWebhookSecret: os.Getenv("JIRA_WEBHOOK_SECRET"),
	}

	if cfg.TelegramToken == "" {
		return nil, errors.New("TELEGRAM_TOKEN is required")
	}

	if cfg.JiraClientID == "" {
		return nil, errors.New("JIRA_CLIENT_ID is required")
	}

	if cfg.JiraClientSecret == "" {
		return nil, errors.New("JIRA_CLIENT_SECRET is required")
	}

	if cfg.EncryptionKey == "" {
		return nil, errors.New("ENCRYPTION_KEY is required (32-byte hex string, 64 characters)")
	}
	if len(cfg.EncryptionKey) != 64 {
		return nil, errors.New("ENCRYPTION_KEY must be exactly 64 hex characters (32 bytes)")
	}

	// JIRA_WEBHOOK_SECRET is optional: Jira Cloud's dynamic-webhook
	// registration API (POST /rest/api/3/webhook) does not expose a
	// per-webhook signing-secret field, so payloads arrive unsigned by
	// default. When the secret is set, the webhook handler will verify
	// X-Hub-Signature; when empty, verification is skipped and the URL
	// should be protected by obscurity / reverse-proxy auth.

	if v := os.Getenv("ADMIN_TELEGRAM_ID"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, errors.New("ADMIN_TELEGRAM_ID must be a valid integer")
		}
		cfg.AdminTelegramID = id
	}

	return cfg, nil
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
