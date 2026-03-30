package config

import (
	"errors"
	"os"
)

type Config struct {
	TelegramToken    string
	MongoURI         string
	MongoDB          string
	LogLevel         string
	JiraClientID     string
	JiraClientSecret string
	JiraRedirectURI  string
	PollInterval       string
	CallbackAddr       string
	EncryptionKey      string
	JiraWebhookSecret  string
}

func Load() (*Config, error) {
	cfg := &Config{
		TelegramToken:    os.Getenv("TELEGRAM_TOKEN"),
		MongoURI:         getEnvOrDefault("MONGO_URI", "mongodb://localhost:27017"),
		MongoDB:          getEnvOrDefault("MONGO_DB", "sleepjirabot"),
		LogLevel:         getEnvOrDefault("LOG_LEVEL", "info"),
		JiraClientID:     os.Getenv("JIRA_CLIENT_ID"),
		JiraClientSecret: os.Getenv("JIRA_CLIENT_SECRET"),
		JiraRedirectURI:  getEnvOrDefault("JIRA_REDIRECT_URI", "http://localhost:8080/callback"),
		PollInterval:      getEnvOrDefault("POLL_INTERVAL", "2m"),
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

	if cfg.JiraWebhookSecret == "" {
		return nil, errors.New("JIRA_WEBHOOK_SECRET is required for webhook signature verification")
	}

	return cfg, nil
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
