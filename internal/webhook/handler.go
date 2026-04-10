package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/rs/zerolog"

	"SleepJiraBot/internal/format"
	"SleepJiraBot/internal/locale"
	"SleepJiraBot/internal/notifydedup"
	"SleepJiraBot/internal/storage"
)

const (
	maxWebhookBodySize     = 1 << 20 // 1 MB
	maxConcurrentJobs      = 10
	eventQueueSize         = 100
	eventProcessingTimeout = 30 * time.Second
)

type Handler struct {
	subRepo       *storage.SubscriptionRepo
	userRepo      *storage.UserRepo
	tgAPI         *tgbotapi.BotAPI
	webhookSecret string
	log           zerolog.Logger
	sem           chan struct{}
	eventQueue    chan Event
	wg            sync.WaitGroup
	dedup         *notifydedup.Guard
}

func NewHandler(subRepo *storage.SubscriptionRepo, userRepo *storage.UserRepo, tgAPI *tgbotapi.BotAPI, webhookSecret string, log zerolog.Logger, dedup *notifydedup.Guard) *Handler {
	h := &Handler{
		subRepo:       subRepo,
		userRepo:      userRepo,
		tgAPI:         tgAPI,
		webhookSecret: webhookSecret,
		log:           log,
		sem:           make(chan struct{}, maxConcurrentJobs),
		eventQueue:    make(chan Event, eventQueueSize),
		dedup:         dedup,
	}

	return h
}

// Start begins processing queued webhook events. It blocks until ctx is
// cancelled and all in-flight events finish processing.
func (h *Handler) Start(ctx context.Context) {
	h.processQueue(ctx)
	h.wg.Wait()
}

func (h *Handler) processQueue(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			h.drainQueue()
			return
		case event := <-h.eventQueue:
			select {
			case h.sem <- struct{}{}:
			case <-ctx.Done():
				h.processEvent(event)
				h.drainQueue()
				return
			}
			h.wg.Add(1)
			go func(e Event) {
				defer func() {
					<-h.sem
					h.wg.Done()
				}()
				h.processEvent(e)
			}(event)
		}
	}
}

// drainQueue processes remaining events in the queue before shutdown.
func (h *Handler) drainQueue() {
	for {
		select {
		case event := <-h.eventQueue:
			h.processEvent(event)
		default:
			return
		}
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	defer func() { _ = r.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBodySize))
	if err != nil {
		h.log.Error().Err(err).Msg("failed to read webhook body")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if len(body) == maxWebhookBodySize {
		h.log.Warn().Int("size", len(body)).Msg("webhook body hit size limit, payload may be truncated")
	}

	// Signature verification is opt-in: Jira Cloud's dynamic-webhook
	// registration API does not expose a signing-secret field, so
	// payloads arrive unsigned unless the operator has wired a secret in
	// out-of-band (e.g. Connect app). When the secret is empty we trust
	// the URL, which must be protected by other means.
	if h.webhookSecret != "" {
		if !h.verifySignature(body, r.Header.Get("X-Hub-Signature")) {
			h.log.Warn().Msg("webhook signature verification failed")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	var event Event
	if err = json.Unmarshal(body, &event); err != nil {
		h.log.Error().Err(err).Msg("failed to parse webhook event")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	select {
	case h.eventQueue <- event:
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "ok")
	default:
		h.log.Error().
			Str("event", event.WebhookEvent).
			Msg("webhook event queue full, rejecting event")
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}
}

func (h *Handler) verifySignature(body []byte, signature string) bool {
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}

	sig := strings.TrimPrefix(signature, "sha256=")
	mac := hmac.New(sha256.New, []byte(h.webhookSecret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(sig), []byte(expected))
}

func (h *Handler) processEvent(event Event) {
	defer func() {
		if r := recover(); r != nil {
			h.log.Error().
				Interface("panic", r).
				Str("stack", string(debug.Stack())).
				Msg("panic in webhook event processing")
		}
	}()

	eventType := NormalizeEventType(event.WebhookEvent)

	projectKey := ""
	issueKey := ""
	if event.Issue != nil {
		issueKey = event.Issue.Key
		if event.Issue.Fields.Project != nil {
			projectKey = event.Issue.Fields.Project.Key
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), eventProcessingTimeout)
	defer cancel()

	var matched []storage.Subscription
	if projectKey != "" {
		subs, err := h.subRepo.GetActiveByProjectKey(ctx, projectKey)
		if err != nil {
			h.log.Error().Err(err).Msg("failed to get project subscriptions")
		} else {
			matched = append(matched, subs...)
		}
	}
	if issueKey != "" {
		subs, err := h.subRepo.GetActiveByIssueKey(ctx, issueKey)
		if err != nil {
			h.log.Error().Err(err).Msg("failed to get issue subscriptions")
		} else {
			matched = append(matched, subs...)
		}
	}

	// Check for mention subscriptions on comment events.
	if (eventType == "comment_created" || eventType == "comment_updated") && event.Comment != nil {
		mentionSubs := h.findMentionSubscriptions(ctx, event)
		matched = append(matched, mentionSubs...)
	}

	if len(matched) == 0 {
		return
	}

	h.log.Info().
		Str("event", eventType).
		Str("project", projectKey).
		Int("subscribers", len(matched)).
		Msg("notifying subscribers")

	// Deduplicate per chat.
	sent := make(map[int64]bool)
	for i := range matched {
		if sent[matched[i].TelegramChatID] {
			continue
		}

		lang := locale.Default
		u, _ := h.userRepo.GetByTelegramID(ctx, matched[i].TelegramUserID)

		// Skip notifications about changes made by the current user.
		if event.User != nil && event.User.AccountID != "" && u != nil && u.JiraAccountID == event.User.AccountID {
			h.log.Debug().
				Int64("chat_id", matched[i].TelegramChatID).
				Str("issue", issueKey).
				Msg("webhook: skipping self-triggered notification")
			sent[matched[i].TelegramChatID] = true
			continue
		}

		// Dedup key discriminates by event type and (for comments) the
		// comment id, so an issue_updated and a comment_created on the
		// same issue within the guard window don't collapse into one.
		if issueKey != "" {
			dedupKey := issueKey + "|" + eventType
			if event.Comment != nil && event.Comment.ID != "" {
				dedupKey += "|" + event.Comment.ID
			}
			if !h.dedup.Allow(matched[i].TelegramChatID, dedupKey) {
				h.log.Debug().
					Int64("chat_id", matched[i].TelegramChatID).
					Str("issue", issueKey).
					Str("event", eventType).
					Msg("webhook: skipping duplicate notification")
				sent[matched[i].TelegramChatID] = true
				continue
			}
		}

		sent[matched[i].TelegramChatID] = true

		if u != nil && u.Language != "" {
			lang = locale.FromString(u.Language)
		}

		text := h.formatNotification(event, eventType, lang)
		msg := tgbotapi.NewMessage(matched[i].TelegramChatID, text)
		msg.ParseMode = tgbotapi.ModeMarkdown
		msg.DisableWebPagePreview = true

		if _, err := h.tgAPI.Send(msg); err != nil {
			h.log.Error().
				Err(err).
				Int64("chat_id", matched[i].TelegramChatID).
				Msg("failed to send notification")
			continue
		}
	}
}

// findMentionSubscriptions parses comment body for mentions and returns
// matching my_mentions subscriptions. The comment body is Atlassian
// Document Format (ADF) — mention nodes carry the target's account id in
// attrs.id, so we walk the tree rather than trying to regex the JSON.
func (h *Handler) findMentionSubscriptions(ctx context.Context, event Event) []storage.Subscription {
	if event.Comment == nil {
		return nil
	}
	if event.Comment.Body == nil {
		// Jira Cloud v3 webhooks normally deliver comment bodies as ADF.
		// An empty body means we cannot see mentions — log loudly so an
		// operator can investigate (custom app registration, truncated
		// payload, future API change) instead of silently losing the
		// notification.
		issueKey := ""
		if event.Issue != nil {
			issueKey = event.Issue.Key
		}
		h.log.Warn().
			Str("issue", issueKey).
			Str("comment_id", event.Comment.ID).
			Msg("webhook: comment event has nil body, mention detection skipped")
		return nil
	}

	mentionIDs := event.Comment.Body.ExtractMentionIDs()
	if len(mentionIDs) == 0 {
		return nil
	}

	accountIDs := make([]string, 0, len(mentionIDs))
	seen := make(map[string]bool, len(mentionIDs))
	for _, id := range mentionIDs {
		// Skip the comment author — they don't need a notification about their own comment.
		if event.Comment.Author != nil && id == event.Comment.Author.AccountID {
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		accountIDs = append(accountIDs, id)
	}

	if len(accountIDs) == 0 {
		return nil
	}

	// Find users with these Jira account IDs.
	users, err := h.userRepo.GetByJiraAccountIDs(ctx, accountIDs)
	if err != nil {
		h.log.Error().Err(err).Msg("webhook: failed to find users by account IDs")
		return nil
	}
	if len(users) == 0 {
		return nil
	}

	userIDs := make([]int64, 0, len(users))
	for i := range users {
		userIDs = append(userIDs, users[i].TelegramUserID)
	}

	// Find active my_mentions subscriptions for these users.
	subs, err := h.subRepo.GetMentionSubscriptionsByUserIDs(ctx, userIDs)
	if err != nil {
		h.log.Error().Err(err).Msg("webhook: failed to find mention subscriptions")
		return nil
	}

	return subs
}

func (h *Handler) formatNotification(event Event, eventType string, lang locale.Lang) string {

	if event.Issue == nil {
		return locale.T(lang, "notif.event", eventType)
	}

	issue := event.Issue
	issueKey := issue.Key
	summary := issue.Fields.Summary

	var emoji string
	switch eventType {
	case "issue_created":
		emoji = "🆕"
	case "issue_updated":
		emoji = "✏️"
	case "issue_deleted":
		emoji = "🗑"
	case "comment_created", "comment_updated":
		emoji = "💬"
	default:
		emoji = "📋"
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s *%s* %s\n", emoji, issueKey, format.EscapeMarkdown(summary))
	fmt.Fprintf(&sb, "%s: _%s_\n", locale.T(lang, "notif.event_label"), format.EscapeMarkdown(eventType))

	if event.User != nil {
		fmt.Fprintf(&sb, "%s: %s\n", locale.T(lang, "notif.by"), format.EscapeMarkdown(event.User.DisplayName))
	}

	if issue.Fields.Status != nil {
		fmt.Fprintf(&sb, "%s: %s\n", locale.T(lang, "notif.status"), format.EscapeMarkdown(issue.Fields.Status.Name))
	}

	if issue.Fields.Assignee != nil {
		fmt.Fprintf(&sb, "%s: %s\n", locale.T(lang, "notif.assignee"), format.EscapeMarkdown(issue.Fields.Assignee.DisplayName))
	}

	if event.Changelog != nil {
		for _, item := range event.Changelog.Items {
			fieldName := item.Field
			if fieldName == "" {
				fieldName = item.FieldID
			}
			if fieldName == "" {
				continue
			}
			fromVal := item.FromString
			if fromVal == "" {
				fromVal = item.From
			}
			toVal := item.ToString
			if toVal == "" {
				toVal = item.To
			}
			if fromVal == toVal {
				continue
			}
			switch {
			case fromVal != "" && toVal != "":
				fmt.Fprintf(&sb, "%s *%s*: %s → %s\n",
					locale.T(lang, "notif.changed"),
					format.EscapeMarkdown(fieldName),
					format.EscapeMarkdown(fromVal),
					format.EscapeMarkdown(toVal),
				)
			case toVal != "":
				fmt.Fprintf(&sb, "%s *%s*: %s\n",
					locale.T(lang, "notif.changed"),
					format.EscapeMarkdown(fieldName),
					format.EscapeMarkdown(toVal),
				)
			default:
				fmt.Fprintf(&sb, "%s *%s*: %s → %s\n",
					locale.T(lang, "notif.changed"),
					format.EscapeMarkdown(fieldName),
					format.EscapeMarkdown(fromVal),
					format.EscapeMarkdown(locale.T(lang, "notif.cleared")),
				)
			}
		}
	}

	if (eventType == "comment_created" || eventType == "comment_updated") && event.Comment != nil && event.Comment.Author != nil {
		fmt.Fprintf(&sb, "%s: %s\n", locale.T(lang, "notif.comment_by"), format.EscapeMarkdown(event.Comment.Author.DisplayName))
	}

	return sb.String()
}
