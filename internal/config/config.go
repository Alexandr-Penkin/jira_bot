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
	ProxyURL          string

	// Phase 0 of DDD microservices split: event bus alongside the monolith.
	// NatsURL is consulted only when EnableEventPublish is true.
	NatsURL            string
	EnableEventPublish bool

	// Phase 1: when EmbedWebhookServer is false, the monolith no longer
	// registers /webhook on its callback server, does not start the
	// webhook event queue, and skips the daily refresher goroutine. The
	// expectation is that a standalone webhook-svc owns webhook ingress
	// in that configuration. Default true preserves current behaviour.
	EmbedWebhookServer bool

	// WebhookSvcAddr is consulted only by cmd/webhook-svc.
	WebhookSvcAddr string

	// Phase 2: identity-svc TokenLease protocol. InternalAddr is the
	// listener for /internal/lease (kept separate from the public
	// callback server so the lease endpoint is not exposed to Jira
	// callbacks / the world). InternalAuthToken is the shared secret
	// checked as a bearer token; empty disables auth and the listener
	// must be protected at the network layer.
	InternalAddr      string
	InternalAuthToken string

	// IdentitySvcURL, when set, directs monolith consumers to call an
	// external identity-svc over HTTP instead of resolving tokens in
	// process. Empty keeps the embedded LocalProvider path.
	IdentitySvcURL string

	// Phase 3: EmbedPoller controls whether the monolith runs its own
	// Jira polling loop. Set false when subscription-svc takes over
	// polling. Default true preserves current behaviour.
	EmbedPoller bool

	// Phase 4: EmbedScheduler controls whether the monolith runs its
	// cron scheduler. Set false when scheduler-svc takes over.
	EmbedScheduler bool

	// Phase 5: EmbedPreferences controls whether the monolith resolves
	// user preferences in process (embedded LocalProvider over UserRepo)
	// or offloads to a standalone preferences-svc over HTTP. Default
	// true preserves current behaviour.
	EmbedPreferences bool

	// PreferencesSvcURL, when set, directs monolith consumers to call an
	// external preferences-svc over HTTP instead of resolving preferences
	// in process. Empty keeps the embedded path.
	PreferencesSvcURL string

	// PreferencesSvcAddr is consulted only by cmd/preferences-svc.
	PreferencesSvcAddr string

	// DedupRedisURL, when set, points notifydedup at a Redis instance
	// instead of the in-process Guard. Use when running subscription-svc
	// or webhook-svc with more than one replica — in-memory dedup is
	// per-process and will allow a duplicate storm. Format:
	// redis://user:pass@host:port/db.
	DedupRedisURL string

	// Phase 6a: NotifyViaEvents flips the producer-side notifier from
	// direct Telegram API calls to NATS event publishing. When true AND
	// EnableEventPublish is true, poller/scheduler/webhook publish
	// NotifyRequested events; a separate telegram-svc is expected to
	// consume and deliver. Default false preserves direct-send behaviour
	// — flipping to true without a running telegram-svc will silently
	// queue messages in JetStream.
	NotifyViaEvents bool

	// Phase 6 prep: PersistConversationStates swaps the Telegram FSM's
	// default in-memory store for a Mongo-backed one (collection
	// conversation_states, TTL-expired). Opt-in for two reasons — Mongo
	// round-trips per update add ~1ms, and the in-memory path is still
	// fine for a single replica. Default false preserves current
	// behaviour.
	PersistConversationStates bool

	// Phase 7a: OpenTelemetry bootstrap. When OtelExporterEndpoint is
	// non-empty, services install an OTLP/gRPC tracer provider with the
	// given endpoint (e.g. "otel-collector:4317"). Empty disables the
	// SDK entirely — a no-op TracerProvider is installed so
	// `otel.Tracer(...)` calls are safe and allocation-free. OtelServiceName
	// overrides the default service.name resource attribute (each cmd
	// supplies its own default).
	OtelExporterEndpoint string
	OtelServiceName      string
	OtelExporterInsecure bool
}

func Load() (*Config, error) {
	cfg := &Config{
		TelegramToken:        os.Getenv("TELEGRAM_TOKEN"),
		MongoURI:             getEnvOrDefault("MONGO_URI", "mongodb://localhost:27017"),
		MongoDB:              getEnvOrDefault("MONGO_DB", "sleepjirabot"),
		LogLevel:             getEnvOrDefault("LOG_LEVEL", "info"),
		JiraClientID:         os.Getenv("JIRA_CLIENT_ID"),
		JiraClientSecret:     os.Getenv("JIRA_CLIENT_SECRET"),
		JiraRedirectURI:      getEnvOrDefault("JIRA_REDIRECT_URI", "http://localhost:8080/callback"),
		PollInterval:         getEnvOrDefault("POLL_INTERVAL", "30s"),
		BatchWindow:          getEnvOrDefault("BATCH_WINDOW", "1m"),
		CallbackAddr:         getEnvOrDefault("CALLBACK_ADDR", ":8080"),
		EncryptionKey:        os.Getenv("ENCRYPTION_KEY"),
		JiraWebhookSecret:    os.Getenv("JIRA_WEBHOOK_SECRET"),
		ProxyURL:             os.Getenv("PROXY_URL"),
		NatsURL:              getEnvOrDefault("NATS_URL", "nats://localhost:4222"),
		EmbedWebhookServer:   true,
		WebhookSvcAddr:       getEnvOrDefault("WEBHOOK_SVC_ADDR", ":8081"),
		InternalAddr:         getEnvOrDefault("INTERNAL_ADDR", ":9080"),
		InternalAuthToken:    os.Getenv("INTERNAL_AUTH_TOKEN"),
		IdentitySvcURL:       os.Getenv("IDENTITY_SVC_URL"),
		EmbedPoller:          true,
		EmbedScheduler:       true,
		EmbedPreferences:     true,
		PreferencesSvcURL:    os.Getenv("PREFERENCES_SVC_URL"),
		PreferencesSvcAddr:   getEnvOrDefault("PREFERENCES_SVC_ADDR", ":9082"),
		DedupRedisURL:        os.Getenv("DEDUP_REDIS_URL"),
		OtelExporterEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		OtelServiceName:      os.Getenv("OTEL_SERVICE_NAME"),
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

	if v := os.Getenv("ENABLE_EVENT_PUBLISH"); v != "" {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			return nil, errors.New("ENABLE_EVENT_PUBLISH must be a boolean (true/false/1/0)")
		}
		cfg.EnableEventPublish = enabled
	}

	if v := os.Getenv("EMBED_WEBHOOK_SERVER"); v != "" {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			return nil, errors.New("EMBED_WEBHOOK_SERVER must be a boolean (true/false/1/0)")
		}
		cfg.EmbedWebhookServer = enabled
	}

	if v := os.Getenv("EMBED_POLLER"); v != "" {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			return nil, errors.New("EMBED_POLLER must be a boolean (true/false/1/0)")
		}
		cfg.EmbedPoller = enabled
	}

	if v := os.Getenv("EMBED_SCHEDULER"); v != "" {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			return nil, errors.New("EMBED_SCHEDULER must be a boolean (true/false/1/0)")
		}
		cfg.EmbedScheduler = enabled
	}

	if v := os.Getenv("EMBED_PREFERENCES"); v != "" {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			return nil, errors.New("EMBED_PREFERENCES must be a boolean (true/false/1/0)")
		}
		cfg.EmbedPreferences = enabled
	}

	if v := os.Getenv("PERSIST_CONVERSATION_STATES"); v != "" {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			return nil, errors.New("PERSIST_CONVERSATION_STATES must be a boolean (true/false/1/0)")
		}
		cfg.PersistConversationStates = enabled
	}

	if v := os.Getenv("NOTIFY_VIA_EVENTS"); v != "" {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			return nil, errors.New("NOTIFY_VIA_EVENTS must be a boolean (true/false/1/0)")
		}
		cfg.NotifyViaEvents = enabled
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
