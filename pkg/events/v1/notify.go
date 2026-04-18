package eventsv1

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

// NotifyRequested is emitted by any producer (poller, scheduler, webhook)
// that wants a Telegram message delivered. telegram-svc is the single
// consumer. Producers pre-format the text; telegram-svc does not re-dedup
// or re-render — upstream is authoritative.
//
// Phase 6a of the microservices split introduces this event as the
// decoupling seam between "something happened" and "Telegram user saw it".
type NotifyRequested struct {
	// ChatID is the Telegram chat or group to deliver to.
	ChatID int64 `json:"chat_id"`
	// TelegramID is the user the notification is about (may equal ChatID
	// for DMs; different for group chats). Optional.
	TelegramID int64 `json:"telegram_id,omitempty"`
	// Text is the pre-rendered message body. telegram-svc sends it
	// verbatim with ParseMode applied.
	Text string `json:"text"`
	// ParseMode mirrors tgbotapi.ParseMode: "", "Markdown", "MarkdownV2",
	// "HTML". Empty means plain text.
	ParseMode string `json:"parse_mode,omitempty"`
	// DisableWebPagePreview suppresses link unfurling.
	DisableWebPagePreview bool `json:"disable_web_page_preview,omitempty"`
	// DedupKey is a caller-supplied key that makes retries of the same
	// logical notification idempotent at the JetStream level (maps to
	// Nats-Msg-Id). Producers set this to "<context>:<chat_id>:<issue_key>:<change_type>"
	// or similar — collisions across contexts are fine because the subject
	// already scopes to notify.requested.
	DedupKey string `json:"dedup_key"`
	// Reason is a human-readable producer tag ("poller:assigned_to_me",
	// "scheduler:daily_report", "webhook:created") used for telemetry.
	Reason string `json:"reason,omitempty"`
	// RequestedAt is producer-side wall clock (unix milliseconds).
	RequestedAt int64 `json:"requested_at"`
}

func (NotifyRequested) Subject() string { return SubjectNotifyRequested }

// NotifyDelivered is emitted by telegram-svc after a successful send.
// Consumers use it for per-user delivery receipts and SLO dashboards.
type NotifyDelivered struct {
	ChatID        int64  `json:"chat_id"`
	TelegramID    int64  `json:"telegram_id,omitempty"`
	DedupKey      string `json:"dedup_key"`
	TelegramMsgID int64  `json:"telegram_msg_id"`
	DeliveredAt   int64  `json:"delivered_at"`
}

func (NotifyDelivered) Subject() string { return SubjectNotifyDelivered }

func (e NotifyDelivered) IdempotencyKey() string {
	if e.DedupKey == "" {
		return "notify.delivered:" + strconv.FormatInt(e.ChatID, 10) + ":" + strconv.FormatInt(e.DeliveredAt, 10)
	}
	return "notify.delivered:" + e.DedupKey
}

// NotifyFailed is emitted when telegram-svc exhausts retries (or hits a
// terminal error like ChatID blocked). Retryable=false means downstream
// consumers should not attempt to re-queue the same payload.
type NotifyFailed struct {
	ChatID     int64  `json:"chat_id"`
	TelegramID int64  `json:"telegram_id,omitempty"`
	DedupKey   string `json:"dedup_key"`
	Reason     string `json:"reason"`
	Retryable  bool   `json:"retryable"`
	FailedAt   int64  `json:"failed_at"`
}

func (NotifyFailed) Subject() string { return SubjectNotifyFailed }

func (e NotifyFailed) IdempotencyKey() string {
	if e.DedupKey == "" {
		return "notify.failed:" + strconv.FormatInt(e.ChatID, 10) + ":" + strconv.FormatInt(e.FailedAt, 10)
	}
	return "notify.failed:" + e.DedupKey
}

// IdempotencyKey hashes DedupKey into a fixed-length Nats-Msg-Id. The
// raw DedupKey could exceed the 128-byte header limit for long issue
// summaries, so we SHA-256 it.
func (e NotifyRequested) IdempotencyKey() string {
	if e.DedupKey == "" {
		// Fallback: without a dedup key we at least keep chat+timestamp
		// as the identity so accidental double-publish within the same
		// ms still dedupes. Different ms → two deliveries, which matches
		// "no dedup requested" semantics.
		return "notify.requested:" + strconv.FormatInt(e.ChatID, 10) + ":" + strconv.FormatInt(e.RequestedAt, 10)
	}
	sum := sha256.Sum256([]byte(e.DedupKey))
	return "notify.requested:" + hex.EncodeToString(sum[:])
}
