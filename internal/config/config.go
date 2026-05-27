package config

import (
	"errors"
	"os"
	"strconv"
)

type Config struct {
	TelegramToken    string
	MongoURI         string
	MongoDB          string
	LogLevel         string
	JiraClientID     string
	JiraClientSecret string
	JiraRedirectURI  string
	PollInterval     string
	BatchWindow      string
	CallbackAddr     string
	EncryptionKey    string
	// EncryptionKeyPrevious is an optional 32-byte hex secondary key
	// the encryptor falls back to on Decrypt. Use during ENCRYPTION_KEY
	// rotation: deploy with the new key as primary and the old one as
	// previous; remove the env var once the next batch write has
	// re-encrypted every record (or after the affected collection's
	// retention window passes).
	EncryptionKeyPrevious string
	JiraWebhookSecret     string
	// JiraWebhookURL is the public HTTPS endpoint Jira posts events to.
	// Passed in the body of POST /rest/api/3/webhook — Atlassian rejects
	// registrations whose host does not match the OAuth app's configured
	// base URL.
	JiraWebhookURL string
	// AllowUnsignedWebhooks must be explicitly set to true when
	// JiraWebhookSecret is empty — otherwise the webhook handler
	// rejects every POST. Default false so a deploy without a
	// configured secret fails closed instead of silently accepting
	// forged events. Set ALLOW_UNSIGNED_WEBHOOKS=true only when the
	// /webhook URL is genuinely protected by other means (network
	// ACL, reverse-proxy auth, etc.).
	AllowUnsignedWebhooks bool
	AdminTelegramID       int64
	ProxyURL              string

	// NatsURL is the JetStream cluster every service connects to. The
	// event bus is mandatory in the microservice topology — there is no
	// in-process fallback — so a connection failure is fatal at startup.
	NatsURL string

	// WebhookSvcAddr is the HTTP listen address for cmd/webhook-svc.
	WebhookSvcAddr string

	// WebhookSvcURL points admin-stats consumers (telegram-svc) at the
	// webhook-svc /internal/stats endpoint so the events_received counter
	// reflects the process that actually owns webhook ingress.
	// Authenticated with InternalAuthToken.
	WebhookSvcURL string

	// InternalAddr is the listener cmd/identity-svc exposes /internal/lease
	// on (kept off the public callback server). InternalAuthToken is the
	// shared bearer secret checked by identity-svc / preferences-svc /
	// webhook-svc internal endpoints; empty disables auth and the
	// listeners must be protected at the network layer.
	InternalAddr      string
	InternalAuthToken string

	// IdentitySvcURL is the base URL of cmd/identity-svc. Every Jira-token
	// consumer (bot, subscription-svc, scheduler-svc, webhook-svc,
	// telegram-svc) leases tokens through it so identity-svc is the single
	// refresh owner. Required for those services.
	IdentitySvcURL string

	// PreferencesSvcURL is the base URL of cmd/preferences-svc. telegram-svc
	// resolves user preferences through it. Required for telegram-svc.
	PreferencesSvcURL string

	// PreferencesSvcAddr is the HTTP listen address for cmd/preferences-svc.
	PreferencesSvcAddr string

	// DedupRedisURL, when set, points notifydedup at a Redis instance
	// instead of the in-process Guard. Use when running a notifying
	// service (subscription-svc / webhook-svc / bot calendar poller) with
	// more than one replica — in-memory dedup is per-process and will
	// allow a duplicate storm. Format: redis://user:pass@host:port/db.
	DedupRedisURL string

	// PersistConversationStates swaps the Telegram FSM's default in-memory
	// store for a Mongo-backed one (collection conversation_states,
	// TTL-expired). Opt-in: Mongo round-trips per update add ~1ms, and the
	// in-memory path is fine for a single telegram-svc replica.
	PersistConversationStates bool

	// OpenTelemetry bootstrap. When OtelExporterEndpoint is non-empty,
	// services install an OTLP/gRPC tracer + meter provider with the given
	// endpoint (e.g. "otel-collector:4317"). Empty disables the SDK
	// entirely — no-op providers are installed so instrumentation calls
	// stay safe and allocation-free. OtelServiceName overrides the default
	// service.name resource attribute (each cmd supplies its own default).
	OtelExporterEndpoint string
	OtelServiceName      string
	OtelExporterInsecure bool

	// Calendar feed knobs. CalendarPollInterval / CalendarLookahead /
	// CalendarFetchTimeout are duration strings parsed at use site
	// (matches POLL_INTERVAL). CalendarDefaultReminderMin is the seed
	// reminder window when a user adds a calendar URL.
	CalendarPollInterval       string
	CalendarLookahead          string
	CalendarFetchTimeout       string
	CalendarDefaultReminderMin int
	// CalendarNewHorizon caps which freshly-discovered events trigger
	// a "🆕 New event" Telegram notification. Set to a short window so
	// the user doesn't get pinged about every meeting added to the
	// next 24h of their calendar — those still show up in the regular
	// "starts in N min" reminder when the time comes. Empty/invalid
	// falls back to 1h.
	CalendarNewHorizon string
}

func Load() (*Config, error) {
	cfg := &Config{
		TelegramToken:         os.Getenv("TELEGRAM_TOKEN"),
		MongoURI:              getEnvOrDefault("MONGO_URI", "mongodb://localhost:27017"),
		MongoDB:               getEnvOrDefault("MONGO_DB", "sleepjirabot"),
		LogLevel:              getEnvOrDefault("LOG_LEVEL", "info"),
		JiraClientID:          os.Getenv("JIRA_CLIENT_ID"),
		JiraClientSecret:      os.Getenv("JIRA_CLIENT_SECRET"),
		JiraRedirectURI:       getEnvOrDefault("JIRA_REDIRECT_URI", "http://localhost:8080/callback"),
		PollInterval:          getEnvOrDefault("POLL_INTERVAL", "30s"),
		BatchWindow:           getEnvOrDefault("BATCH_WINDOW", "1m"),
		CallbackAddr:          getEnvOrDefault("CALLBACK_ADDR", ":8080"),
		EncryptionKey:         os.Getenv("ENCRYPTION_KEY"),
		EncryptionKeyPrevious: os.Getenv("ENCRYPTION_KEY_PREVIOUS"),
		JiraWebhookSecret:     os.Getenv("JIRA_WEBHOOK_SECRET"),
		JiraWebhookURL:        os.Getenv("JIRA_WEBHOOK_URL"),
		ProxyURL:              os.Getenv("PROXY_URL"),
		NatsURL:               getEnvOrDefault("NATS_URL", "nats://localhost:4222"),
		WebhookSvcAddr:        getEnvOrDefault("WEBHOOK_SVC_ADDR", ":8081"),
		WebhookSvcURL:         os.Getenv("WEBHOOK_SVC_URL"),
		InternalAddr:          getEnvOrDefault("INTERNAL_ADDR", ":9080"),
		InternalAuthToken:     os.Getenv("INTERNAL_AUTH_TOKEN"),
		IdentitySvcURL:        os.Getenv("IDENTITY_SVC_URL"),
		PreferencesSvcURL:     os.Getenv("PREFERENCES_SVC_URL"),
		PreferencesSvcAddr:    getEnvOrDefault("PREFERENCES_SVC_ADDR", ":9082"),
		DedupRedisURL:         os.Getenv("DEDUP_REDIS_URL"),
		OtelExporterEndpoint:  os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		OtelServiceName:       os.Getenv("OTEL_SERVICE_NAME"),

		CalendarPollInterval:       getEnvOrDefault("CALENDAR_POLL_INTERVAL", "5m"),
		CalendarLookahead:          getEnvOrDefault("CALENDAR_LOOKAHEAD", "24h"),
		CalendarFetchTimeout:       getEnvOrDefault("CALENDAR_FETCH_TIMEOUT", "30s"),
		CalendarNewHorizon:         getEnvOrDefault("CALENDAR_NEW_HORIZON", "1h"),
		CalendarDefaultReminderMin: 15,
	}

	if v := os.Getenv("CALENDAR_DEFAULT_REMINDER_MIN"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1440 {
			return nil, errors.New("CALENDAR_DEFAULT_REMINDER_MIN must be an integer in [1, 1440]")
		}
		cfg.CalendarDefaultReminderMin = n
	}

	if v := os.Getenv("OTEL_EXPORTER_OTLP_INSECURE"); v != "" {
		insecure, err := strconv.ParseBool(v)
		if err != nil {
			return nil, errors.New("OTEL_EXPORTER_OTLP_INSECURE must be a boolean (true/false/1/0)")
		}
		cfg.OtelExporterInsecure = insecure
	} else {
		cfg.OtelExporterInsecure = true
	}

	if v := os.Getenv("PERSIST_CONVERSATION_STATES"); v != "" {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			return nil, errors.New("PERSIST_CONVERSATION_STATES must be a boolean (true/false/1/0)")
		}
		cfg.PersistConversationStates = enabled
	}

	if v := os.Getenv("ALLOW_UNSIGNED_WEBHOOKS"); v != "" {
		allow, err := strconv.ParseBool(v)
		if err != nil {
			return nil, errors.New("ALLOW_UNSIGNED_WEBHOOKS must be a boolean (true/false/1/0)")
		}
		cfg.AllowUnsignedWebhooks = allow
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
	if cfg.EncryptionKeyPrevious != "" && len(cfg.EncryptionKeyPrevious) != 64 {
		return nil, errors.New("ENCRYPTION_KEY_PREVIOUS must be exactly 64 hex characters (32 bytes) when set")
	}

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

// ValidateWebhookAuth ensures the webhook endpoint has some form of
// authentication configured. Called once at startup by cmd/webhook-svc,
// the only binary that serves /webhook.
func (c *Config) ValidateWebhookAuth() error {
	if c.JiraWebhookSecret == "" && !c.AllowUnsignedWebhooks {
		return errors.New("JIRA_WEBHOOK_SECRET is empty and ALLOW_UNSIGNED_WEBHOOKS=true was not set; refusing to expose /webhook with unauthenticated ingress")
	}
	return nil
}
