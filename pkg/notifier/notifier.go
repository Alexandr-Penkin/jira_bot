// Package notifier is the delivery seam between producer services
// (poller, scheduler, webhook) and the Telegram Bot API.
//
// Phase 6a of the DDD split replaces scattered tgbotapi.Send() calls
// with a single Notifier interface so the delivery path can be swapped
// from in-process (DirectNotifier) to event-driven (EventNotifier) via
// config, without changing producer code.
package notifier

import (
	"context"
	"errors"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/rs/zerolog"

	eventsv1 "SleepJiraBot/pkg/events/v1"
)

// Request is the neutral, wire-serializable form of a Telegram outgoing
// message. It deliberately omits anything that cannot round-trip through
// JSON (inline keyboards etc.) — the direct-send call sites in
// poller/scheduler/webhook do not use those.
type Request struct {
	ChatID                int64
	TelegramID            int64
	Text                  string
	ParseMode             string
	DisableWebPagePreview bool
	DedupKey              string
	Reason                string
}

// Notifier is the producer-side abstraction. One Send call equals one
// user-visible notification attempt. Implementations decide whether
// delivery happens in-process or via events.
type Notifier interface {
	Send(ctx context.Context, req Request) error
}

// DirectNotifier writes straight to the Telegram Bot API. This is the
// default: same behaviour as the pre-Phase-6 monolith and the current
// subscription-svc / scheduler-svc / webhook-svc containers.
type DirectNotifier struct {
	api *tgbotapi.BotAPI
	log zerolog.Logger
}

// NewDirect wraps a tgbotapi client. The log is used only for failures
// — successful sends stay quiet so call sites own the happy-path log.
func NewDirect(api *tgbotapi.BotAPI, log zerolog.Logger) *DirectNotifier {
	return &DirectNotifier{api: api, log: log}
}

func (d *DirectNotifier) Send(_ context.Context, req Request) error {
	if d.api == nil {
		return errors.New("notifier: direct: nil telegram api")
	}
	msg := tgbotapi.NewMessage(req.ChatID, req.Text)
	if req.ParseMode != "" {
		msg.ParseMode = req.ParseMode
	}
	msg.DisableWebPagePreview = req.DisableWebPagePreview
	_, err := d.api.Send(msg)
	return err
}

// EventNotifier publishes NotifyRequested events to NATS; a downstream
// telegram-svc consumer turns them into Telegram messages. Use when the
// bot-send concern has been extracted into its own process.
type EventNotifier struct {
	pub eventsv1.Publisher
	log zerolog.Logger
}

// NewEvent wraps a Publisher. Pass eventsv1.NoopPublisher{} when events
// are disabled — Send will silently no-op (notifications are dropped),
// so production deployments should always pair this with a real
// JetStream publisher and a running telegram-svc.
func NewEvent(pub eventsv1.Publisher, log zerolog.Logger) *EventNotifier {
	return &EventNotifier{pub: pub, log: log}
}

func (e *EventNotifier) Send(ctx context.Context, req Request) error {
	if e.pub == nil {
		return errors.New("notifier: event: nil publisher")
	}
	evt := eventsv1.NotifyRequested{
		ChatID:                req.ChatID,
		TelegramID:            req.TelegramID,
		Text:                  req.Text,
		ParseMode:             req.ParseMode,
		DisableWebPagePreview: req.DisableWebPagePreview,
		DedupKey:              req.DedupKey,
		Reason:                req.Reason,
		RequestedAt:           time.Now().UnixMilli(),
	}
	return e.pub.Publish(ctx, evt, "")
}
