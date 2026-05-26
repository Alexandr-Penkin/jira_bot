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

func TestLoad_IdentitySvcWiring(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("IDENTITY_SVC_URL", "http://identity-svc:9080")
	t.Setenv("INTERNAL_AUTH_TOKEN", "secret-bearer")
	t.Setenv("INTERNAL_ADDR", ":9999")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "http://identity-svc:9080", cfg.IdentitySvcURL)
	assert.Equal(t, "secret-bearer", cfg.InternalAuthToken)
	assert.Equal(t, ":9999", cfg.InternalAddr)
}

func TestLoad_IdentitySvcDefaults(t *testing.T) {
	setRequiredEnv(t)
	os.Unsetenv("IDENTITY_SVC_URL")
	os.Unsetenv("INTERNAL_AUTH_TOKEN")
	os.Unsetenv("INTERNAL_ADDR")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Empty(t, cfg.IdentitySvcURL)
	assert.Empty(t, cfg.InternalAuthToken)
	assert.Equal(t, ":9080", cfg.InternalAddr)
}

func TestLoad_NatsURLDefault(t *testing.T) {
	setRequiredEnv(t)
	os.Unsetenv("NATS_URL")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "nats://localhost:4222", cfg.NatsURL)
}

func TestLoad_WebhookSvcAddrDefault(t *testing.T) {
	setRequiredEnv(t)
	os.Unsetenv("WEBHOOK_SVC_ADDR")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, ":8081", cfg.WebhookSvcAddr)
}

func TestLoad_AdminTelegramIDParsed(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ADMIN_TELEGRAM_ID", "12345")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, int64(12345), cfg.AdminTelegramID)
}

func TestLoad_AdminTelegramIDInvalid(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ADMIN_TELEGRAM_ID", "not-a-number")

	cfg, err := Load()
	assert.Nil(t, cfg)
	assert.EqualError(t, err, "ADMIN_TELEGRAM_ID must be a valid integer")
}

func TestLoad_AdminTelegramIDEmptyLeavesZero(t *testing.T) {
	setRequiredEnv(t)
	os.Unsetenv("ADMIN_TELEGRAM_ID")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, int64(0), cfg.AdminTelegramID)
}

func TestLoad_WebhookSecretAllowedWithOptIn(t *testing.T) {
	// Operators who explicitly accept the risk can keep running unsigned.
	t.Setenv("TELEGRAM_TOKEN", "test-token")
	t.Setenv("JIRA_CLIENT_ID", "cid")
	t.Setenv("JIRA_CLIENT_SECRET", "csecret")
	t.Setenv("ENCRYPTION_KEY", testEncryptionKey)
	t.Setenv("JIRA_WEBHOOK_SECRET", "")
	t.Setenv("ALLOW_UNSIGNED_WEBHOOKS", "true")

	cfg, err := Load()
	require.NoError(t, err)
	assert.True(t, cfg.AllowUnsignedWebhooks)
	assert.Empty(t, cfg.JiraWebhookSecret)
}

func TestValidateWebhookAuth(t *testing.T) {
	// webhook-svc is the only binary that serves /webhook and calls this
	// directly at startup. A missing secret without an explicit opt-in
	// must fail closed; a secret or the opt-in passes.
	t.Run("empty secret, no opt-in -> error", func(t *testing.T) {
		c := &Config{JiraWebhookSecret: "", AllowUnsignedWebhooks: false}
		assert.ErrorContains(t, c.ValidateWebhookAuth(), "JIRA_WEBHOOK_SECRET is empty")
	})
	t.Run("secret set -> ok", func(t *testing.T) {
		c := &Config{JiraWebhookSecret: "s3cret"}
		assert.NoError(t, c.ValidateWebhookAuth())
	})
	t.Run("opt-in unsigned -> ok", func(t *testing.T) {
		c := &Config{JiraWebhookSecret: "", AllowUnsignedWebhooks: true}
		assert.NoError(t, c.ValidateWebhookAuth())
	})
}
