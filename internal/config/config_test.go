package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 64 hex chars = 32 bytes
const testEncryptionKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("TELEGRAM_TOKEN", "test-token")
	t.Setenv("JIRA_CLIENT_ID", "test-client-id")
	t.Setenv("JIRA_CLIENT_SECRET", "test-client-secret")
	t.Setenv("ENCRYPTION_KEY", testEncryptionKey)
	t.Setenv("JIRA_WEBHOOK_SECRET", "test-webhook-secret")
}

func TestLoad_Success(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "test-token", cfg.TelegramToken)
	assert.Equal(t, "test-client-id", cfg.JiraClientID)
	assert.Equal(t, "test-client-secret", cfg.JiraClientSecret)
	assert.Equal(t, testEncryptionKey, cfg.EncryptionKey)
}

func TestLoad_Defaults(t *testing.T) {
	setRequiredEnv(t)

	os.Unsetenv("MONGO_URI")
	os.Unsetenv("MONGO_DB")
	os.Unsetenv("LOG_LEVEL")
	os.Unsetenv("JIRA_REDIRECT_URI")
	os.Unsetenv("CALLBACK_ADDR")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "mongodb://localhost:27017", cfg.MongoURI)
	assert.Equal(t, "sleepjirabot", cfg.MongoDB)
	assert.Equal(t, "info", cfg.LogLevel)
	assert.Equal(t, "http://localhost:8080/callback", cfg.JiraRedirectURI)
	assert.Equal(t, ":8080", cfg.CallbackAddr)
}

func TestLoad_CustomEnvValues(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("MONGO_URI", "mongodb://custom:27017")
	t.Setenv("MONGO_DB", "mydb")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("JIRA_REDIRECT_URI", "https://example.com/callback")
	t.Setenv("CALLBACK_ADDR", ":9090")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "mongodb://custom:27017", cfg.MongoURI)
	assert.Equal(t, "mydb", cfg.MongoDB)
	assert.Equal(t, "debug", cfg.LogLevel)
	assert.Equal(t, "https://example.com/callback", cfg.JiraRedirectURI)
	assert.Equal(t, ":9090", cfg.CallbackAddr)
}

func TestLoad_MissingTelegramToken(t *testing.T) {
	t.Setenv("TELEGRAM_TOKEN", "")
	t.Setenv("JIRA_CLIENT_ID", "cid")
	t.Setenv("JIRA_CLIENT_SECRET", "csecret")
	t.Setenv("ENCRYPTION_KEY", testEncryptionKey)

	cfg, err := Load()
	assert.Nil(t, cfg)
	assert.EqualError(t, err, "TELEGRAM_TOKEN is required")
}

func TestLoad_MissingJiraClientID(t *testing.T) {
	t.Setenv("TELEGRAM_TOKEN", "tok")
	t.Setenv("JIRA_CLIENT_ID", "")
	t.Setenv("JIRA_CLIENT_SECRET", "csecret")
	t.Setenv("ENCRYPTION_KEY", testEncryptionKey)

	cfg, err := Load()
	assert.Nil(t, cfg)
	assert.EqualError(t, err, "JIRA_CLIENT_ID is required")
}

func TestLoad_MissingJiraClientSecret(t *testing.T) {
	t.Setenv("TELEGRAM_TOKEN", "tok")
	t.Setenv("JIRA_CLIENT_ID", "cid")
	t.Setenv("JIRA_CLIENT_SECRET", "")
	t.Setenv("ENCRYPTION_KEY", testEncryptionKey)

	cfg, err := Load()
	assert.Nil(t, cfg)
	assert.EqualError(t, err, "JIRA_CLIENT_SECRET is required")
}

func TestLoad_MissingEncryptionKey(t *testing.T) {
	t.Setenv("TELEGRAM_TOKEN", "tok")
	t.Setenv("JIRA_CLIENT_ID", "cid")
	t.Setenv("JIRA_CLIENT_SECRET", "csecret")
	t.Setenv("ENCRYPTION_KEY", "")

	cfg, err := Load()
	assert.Nil(t, cfg)
	assert.EqualError(t, err, "ENCRYPTION_KEY is required (32-byte hex string, 64 characters)")
}

func TestLoad_WrongLengthEncryptionKey(t *testing.T) {
	t.Setenv("TELEGRAM_TOKEN", "tok")
	t.Setenv("JIRA_CLIENT_ID", "cid")
	t.Setenv("JIRA_CLIENT_SECRET", "csecret")
	t.Setenv("ENCRYPTION_KEY", "0123456789abcdef")

	cfg, err := Load()
	assert.Nil(t, cfg)
	assert.EqualError(t, err, "ENCRYPTION_KEY must be exactly 64 hex characters (32 bytes)")
}

func TestGetEnvOrDefault(t *testing.T) {
	t.Setenv("TEST_KEY_EXISTS", "value123")

	assert.Equal(t, "value123", getEnvOrDefault("TEST_KEY_EXISTS", "default"))
	assert.Equal(t, "default", getEnvOrDefault("TEST_KEY_DOES_NOT_EXIST", "default"))
}
