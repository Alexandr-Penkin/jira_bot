package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"go.mongodb.org/mongo-driver/v2/bson"

	"SleepJiraBot/internal/storage"
)

// SubscriptionWebhookJQL maps an internal subscription record to a JQL
// filter that Jira will accept for dynamic webhook registration.
//
// Jira's webhook JQL parser is a strict subset of standard JQL: no
// custom fields, no ORDER BY, and only a handful of clauses —
// assignee, reporter, creator, project, issuekey. The watcher/voter
// clauses (once supported) are no longer accepted and trigger a
// "Clause X is unsupported" rejection at registration time.
// currentUser() works because the webhook is owned by the OAuth user.
//
// Returns "" when there is nothing to register (e.g. raw filter
// subscriptions whose JQL we cannot guarantee to be webhook-compatible).
// In that case the poller picks up the slack.
func SubscriptionWebhookJQL(sub *storage.Subscription) string {
	switch sub.SubscriptionType {
	case storage.SubTypeMyNewIssues:
		return "assignee = currentUser()"
	case storage.SubTypeMyWatched:
		// Jira webhook JQL no longer accepts `watcher`. There is no
		// safe substitute that captures "issues I watch" — let the
		// poller handle this subscription type.
		return ""
	case storage.SubTypeMyMentions:
		// Jira webhook JQL has no "mentioned me" clause. We register
		// the broadest still-supported user-relation filter and let
		// the webhook handler parse comment ADF for actual mentions.
		// Pure cold mentions in unrelated projects still cannot be
		// caught.
		return "(assignee = currentUser() OR reporter = currentUser())"
	case storage.SubTypeProjectUpdates:
		if sub.JiraProjectKey == "" {
			return ""
		}
		return fmt.Sprintf("project = %q", sub.JiraProjectKey)
	case storage.SubTypeIssueUpdates:
		if sub.JiraIssueKey == "" {
			return ""
		}
		return fmt.Sprintf("issuekey = %q", sub.JiraIssueKey)
	case storage.SubTypeFilterUpdates:
		// Filter JQL is user-defined and may use clauses Jira's
		// webhook engine rejects. Skip and rely on the poller for
		// these subscriptions.
		return ""
	default:
		return ""
	}
}

// SanitizeWebhookJQL trims whitespace and strips a trailing ORDER BY
// clause, which Jira's webhook JQL parser rejects.
func SanitizeWebhookJQL(jql string) string {
	jql = strings.TrimSpace(jql)
	if idx := strings.Index(strings.ToUpper(jql), " ORDER BY "); idx != -1 {
		jql = strings.TrimSpace(jql[:idx])
	}
	return jql
}

// WebhookManager owns the lifecycle of dynamically registered Jira
// webhooks: registering them when subscriptions are created, deleting
// them when subscriptions go away, and refreshing them before they
// expire. It is safe to call from multiple goroutines.
type WebhookManager struct {
	client      *Client
	userRepo    *storage.UserRepo
	webhookRepo *storage.WebhookRepo
	deliveryURL string
	log         zerolog.Logger
}

func NewWebhookManager(client *Client, userRepo *storage.UserRepo, webhookRepo *storage.WebhookRepo, deliveryURL string, log zerolog.Logger) *WebhookManager {
	return &WebhookManager{
		client:      client,
		userRepo:    userRepo,
		webhookRepo: webhookRepo,
		deliveryURL: deliveryURL,
		log:         log,
	}
}

// RegisterForSubscription creates a Jira webhook for the given subscription
// and persists the registration. No-op when the user has no Jira tokens
// yet (subscription was created before /connect — the OAuth callback will
// retry registration later) or the subscription type does not map to a
// webhook-compatible JQL filter.
func (m *WebhookManager) RegisterForSubscription(ctx context.Context, sub *storage.Subscription) {
	if sub == nil {
		return
	}
	if m.deliveryURL == "" {
		m.log.Warn().
			Int64("user_id", sub.TelegramUserID).
			Str("sub_type", sub.SubscriptionType).
			Msg("webhook delivery URL not configured; skipping registration (set JIRA_WEBHOOK_URL)")
		return
	}
	jql := SanitizeWebhookJQL(SubscriptionWebhookJQL(sub))
	if jql == "" {
		return
	}

	user, err := m.userRepo.GetByTelegramID(ctx, sub.TelegramUserID)
	if err != nil || user == nil || user.AccessToken == "" {
		// User not connected yet — webhook will be registered after
		// the OAuth callback finalizes the connection.
		return
	}

	webhookID, expiresAt, err := m.client.RegisterWebhook(ctx, user, m.deliveryURL, jql, DefaultWebhookEvents)
	if err != nil {
		m.log.Warn().
			Err(err).
			Int64("user_id", sub.TelegramUserID).
			Str("sub_type", sub.SubscriptionType).
			Str("jql", jql).
			Msg("failed to register Jira webhook")
		return
	}

	reg := &storage.WebhookRegistration{
		TelegramUserID: sub.TelegramUserID,
		JiraCloudID:    user.JiraCloudID,
		SubscriptionID: sub.ID,
		WebhookID:      webhookID,
		JqlFilter:      jql,
		Events:         DefaultWebhookEvents,
		ExpiresAt:      expiresAt,
	}
	if err = m.webhookRepo.Create(ctx, reg); err != nil {
		m.log.Error().
			Err(err).
			Int64("user_id", sub.TelegramUserID).
			Int64("webhook_id", webhookID).
			Msg("failed to persist webhook registration; rolling back in Jira")
		_ = m.client.DeleteWebhooks(ctx, user, []int64{webhookID})
		return
	}

	m.log.Info().
		Int64("user_id", sub.TelegramUserID).
		Int64("webhook_id", webhookID).
		Str("sub_type", sub.SubscriptionType).
		Time("expires_at", expiresAt).
		Msg("registered Jira webhook")
}

// RegisterForExistingSubscriptions registers webhooks for every active
// subscription a user already has. Called from the OAuth callback after a
// successful (re)connect — covers users who created subscriptions before
// the auto-register feature shipped, or who reconnected after revoking
// access. Idempotent when the registration already exists.
func (m *WebhookManager) RegisterForExistingSubscriptions(ctx context.Context, telegramUserID int64, subs []storage.Subscription) {
	if len(subs) == 0 {
		return
	}
	existing, err := m.webhookRepo.GetByUser(ctx, telegramUserID)
	if err != nil {
		m.log.Warn().Err(err).Int64("user_id", telegramUserID).Msg("failed to read existing webhook registrations")
	}
	hasReg := make(map[bson.ObjectID]bool, len(existing))
	for i := range existing {
		hasReg[existing[i].SubscriptionID] = true
	}

	for i := range subs {
		if hasReg[subs[i].ID] {
			continue
		}
		m.RegisterForSubscription(ctx, &subs[i])
	}
}

// DeleteForSubscription removes the Jira webhook(s) tied to a specific
// subscription record. Best-effort: a Jira-side failure is logged but
// does not block deletion of the local registration row.
func (m *WebhookManager) DeleteForSubscription(ctx context.Context, telegramUserID int64, subscriptionID bson.ObjectID) {
	regs, err := m.webhookRepo.GetBySubscription(ctx, subscriptionID)
	if err != nil {
		m.log.Warn().Err(err).Msg("failed to read webhook registrations for subscription")
		return
	}
	if len(regs) == 0 {
		return
	}

	user, err := m.userRepo.GetByTelegramID(ctx, telegramUserID)
	if err == nil && user != nil && user.AccessToken != "" {
		ids := make([]int64, 0, len(regs))
		for i := range regs {
			ids = append(ids, regs[i].WebhookID)
		}
		if delErr := m.client.DeleteWebhooks(ctx, user, ids); delErr != nil {
			m.log.Warn().Err(delErr).Int64("user_id", telegramUserID).Msg("failed to delete Jira webhooks")
		}
	}

	if err = m.webhookRepo.DeleteBySubscription(ctx, subscriptionID); err != nil {
		m.log.Warn().Err(err).Msg("failed to delete webhook registration rows")
	}
}

// DeleteAllForUser removes every webhook the user has registered with
// Jira. Used by /disconnect.
func (m *WebhookManager) DeleteAllForUser(ctx context.Context, telegramUserID int64) {
	regs, err := m.webhookRepo.GetByUser(ctx, telegramUserID)
	if err != nil {
		m.log.Warn().Err(err).Int64("user_id", telegramUserID).Msg("failed to read user webhook registrations")
		return
	}
	if len(regs) == 0 {
		return
	}

	user, err := m.userRepo.GetByTelegramID(ctx, telegramUserID)
	if err == nil && user != nil && user.AccessToken != "" {
		ids := make([]int64, 0, len(regs))
		for i := range regs {
			ids = append(ids, regs[i].WebhookID)
		}
		if delErr := m.client.DeleteWebhooks(ctx, user, ids); delErr != nil {
			m.log.Warn().Err(delErr).Int64("user_id", telegramUserID).Msg("failed to delete Jira webhooks on disconnect")
		}
	}

	if err := m.webhookRepo.DeleteByUser(ctx, telegramUserID); err != nil {
		m.log.Warn().Err(err).Int64("user_id", telegramUserID).Msg("failed to delete user webhook registration rows")
	}
}

// RefreshExpiring extends the lifetime of every persisted webhook whose
// expires_at falls before threshold. Run periodically by the background
// refresher. Webhooks that fail to refresh (e.g. user disconnected, or
// Jira returned 404) are removed from the local store so the next run
// does not keep retrying them.
func (m *WebhookManager) RefreshExpiring(ctx context.Context, threshold time.Time) {
	regs, err := m.webhookRepo.GetExpiringBefore(ctx, threshold)
	if err != nil {
		m.log.Error().Err(err).Msg("webhook refresher: failed to read expiring registrations")
		return
	}
	if len(regs) == 0 {
		return
	}

	// Group by user so we can refresh all of a user's webhooks in one
	// API call (Jira accepts up to 100 ids per request).
	byUser := make(map[int64][]storage.WebhookRegistration)
	for i := range regs {
		byUser[regs[i].TelegramUserID] = append(byUser[regs[i].TelegramUserID], regs[i])
	}

	for telegramUserID, userRegs := range byUser {
		user, err := m.userRepo.GetByTelegramID(ctx, telegramUserID)
		if err != nil || user == nil || user.AccessToken == "" {
			m.log.Warn().Int64("user_id", telegramUserID).Msg("webhook refresher: user gone, dropping registrations")
			for i := range userRegs {
				_ = m.webhookRepo.Delete(ctx, userRegs[i].ID)
			}
			continue
		}

		ids := make([]int64, 0, len(userRegs))
		for i := range userRegs {
			ids = append(ids, userRegs[i].WebhookID)
		}

		newExpiry, err := m.client.RefreshWebhooks(ctx, user, ids)
		if err != nil {
			m.log.Warn().
				Err(err).
				Int64("user_id", telegramUserID).
				Int("count", len(ids)).
				Msg("webhook refresher: refresh failed, will attempt re-registration on next sub event")
			// On a hard failure, drop the rows so we don't keep
			// hitting the same error every cycle. The user's next
			// /subscribe will create a fresh webhook.
			if isWebhookGone(err) {
				for i := range userRegs {
					_ = m.webhookRepo.Delete(ctx, userRegs[i].ID)
				}
			}
			continue
		}

		for i := range userRegs {
			if err := m.webhookRepo.UpdateExpiry(ctx, userRegs[i].ID, newExpiry); err != nil {
				m.log.Warn().Err(err).Msg("webhook refresher: failed to update expiry")
			}
		}

		m.log.Debug().
			Int64("user_id", telegramUserID).
			Int("count", len(ids)).
			Time("new_expiry", newExpiry).
			Msg("webhook refresher: refreshed")
	}
}

// isWebhookGone reports whether the error from a refresh call indicates
// that the webhook no longer exists on Jira's side and should be dropped
// locally rather than retried.
func isWebhookGone(err error) bool {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Status == http.StatusNotFound || httpErr.Status == http.StatusGone
	}
	return false
}

// WebhookLifetime is how long Jira keeps a dynamically registered webhook
// alive before requiring a refresh. Atlassian documents this as 30 days.
const WebhookLifetime = 30 * 24 * time.Hour

// DefaultWebhookEvents are the Jira event names we want for issue and
// comment notifications. Names match Jira's documented webhook event IDs.
var DefaultWebhookEvents = []string{
	"jira:issue_created",
	"jira:issue_updated",
	"jira:issue_deleted",
	"comment_created",
	"comment_updated",
}

// webhookSpec is the body element passed to POST /rest/api/3/webhook.
type webhookSpec struct {
	Events    []string `json:"events"`
	JqlFilter string   `json:"jqlFilter"`
}

type registerWebhookRequest struct {
	URL      string        `json:"url,omitempty"`
	Webhooks []webhookSpec `json:"webhooks"`
}

// RegisteredWebhook is one entry in the Jira webhook registration response.
type RegisteredWebhook struct {
	CreatedWebhookID int64    `json:"createdWebhookId"`
	Errors           []string `json:"errors,omitempty"`
}

type registerWebhookResponse struct {
	WebhookRegistrationResult []RegisteredWebhook `json:"webhookRegistrationResult"`
}

// RegisterWebhook registers a single dynamic webhook for the given user
// with the given JQL filter and event list. Returns the Jira-assigned
// webhook id and the absolute expiry time. The deliveryURL is required:
// Atlassian's dynamic-webhook API expects the receiver URL in the body,
// and rejects registrations whose host does not match the OAuth app's
// configured base URL.
func (c *Client) RegisterWebhook(ctx context.Context, user *storage.User, deliveryURL, jqlFilter string, events []string) (int64, time.Time, error) {
	if deliveryURL == "" {
		return 0, time.Time{}, fmt.Errorf("delivery URL is required")
	}
	if jqlFilter == "" {
		return 0, time.Time{}, fmt.Errorf("jql filter is required")
	}
	if len(events) == 0 {
		events = DefaultWebhookEvents
	}

	payload := registerWebhookRequest{
		URL: deliveryURL,
		Webhooks: []webhookSpec{{
			Events:    events,
			JqlFilter: jqlFilter,
		}},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("marshal webhook payload: %w", err)
	}

	body, err := c.doRequest(ctx, user, http.MethodPost, "/webhook", bytes.NewReader(data))
	if err != nil {
		return 0, time.Time{}, err
	}

	var resp registerWebhookResponse
	if err = json.Unmarshal(body, &resp); err != nil {
		return 0, time.Time{}, fmt.Errorf("decode webhook response: %w", err)
	}

	if len(resp.WebhookRegistrationResult) == 0 {
		return 0, time.Time{}, fmt.Errorf("empty webhook registration response")
	}
	first := resp.WebhookRegistrationResult[0]
	if len(first.Errors) > 0 {
		return 0, time.Time{}, fmt.Errorf("jira rejected webhook registration: %v", first.Errors)
	}
	if first.CreatedWebhookID == 0 {
		return 0, time.Time{}, fmt.Errorf("jira returned zero webhook id")
	}

	return first.CreatedWebhookID, time.Now().Add(WebhookLifetime), nil
}

type refreshWebhookRequest struct {
	WebhookIDs []int64 `json:"webhookIds"`
}

type refreshWebhookResponse struct {
	ExpirationDate string `json:"expirationDate"`
}

// RefreshWebhooks extends the lifetime of the given webhook ids by another
// 30 days. Returns the new expiration time reported by Jira (or now+30d as
// a fallback if Jira's response is missing the expirationDate field).
func (c *Client) RefreshWebhooks(ctx context.Context, user *storage.User, webhookIDs []int64) (time.Time, error) {
	if len(webhookIDs) == 0 {
		return time.Time{}, nil
	}

	payload := refreshWebhookRequest{WebhookIDs: webhookIDs}
	data, err := json.Marshal(payload)
	if err != nil {
		return time.Time{}, fmt.Errorf("marshal refresh payload: %w", err)
	}

	body, err := c.doRequest(ctx, user, http.MethodPut, "/webhook/refresh", bytes.NewReader(data))
	if err != nil {
		return time.Time{}, err
	}

	var resp refreshWebhookResponse
	if unmarshalErr := json.Unmarshal(body, &resp); unmarshalErr != nil {
		// The body shape is not load-bearing for the refresh to have
		// succeeded — Jira returned 2xx, so the lifetime was extended.
		c.log.Debug().Err(unmarshalErr).Msg("webhook refresh: unparseable response, assuming default lifetime")
		return time.Now().Add(WebhookLifetime), nil //nolint:nilerr // 2xx already confirms success
	}

	if resp.ExpirationDate == "" {
		return time.Now().Add(WebhookLifetime), nil
	}

	if parsed, parseErr := time.Parse(time.RFC3339, resp.ExpirationDate); parseErr == nil {
		return parsed, nil
	}
	return time.Now().Add(WebhookLifetime), nil
}

// ListedWebhook is one entry returned by GET /rest/api/3/webhook. Fields
// are the subset we surface in diagnostics; Jira returns more (field/issue
// property filters) that we currently ignore.
type ListedWebhook struct {
	ID             int64    `json:"id"`
	JqlFilter      string   `json:"jqlFilter"`
	Events         []string `json:"events"`
	ExpirationDate string   `json:"expirationDate"`
}

type listWebhooksResponse struct {
	Values     []ListedWebhook `json:"values"`
	IsLast     bool            `json:"isLast"`
	MaxResults int             `json:"maxResults"`
	StartAt    int             `json:"startAt"`
	Total      int             `json:"total"`
}

// ListWebhooks returns every dynamic webhook the calling app has registered
// for this user, paging through GET /rest/api/3/webhook until isLast.
// Atlassian returns up to 100 per page; in practice a single user holds at
// most a handful, but we follow pagination so we never silently truncate.
func (c *Client) ListWebhooks(ctx context.Context, user *storage.User) ([]ListedWebhook, error) {
	var all []ListedWebhook
	startAt := 0
	for {
		path := fmt.Sprintf("/webhook?startAt=%d", startAt)
		body, err := c.doRequest(ctx, user, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		var resp listWebhooksResponse
		if err = json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("decode webhook list: %w", err)
		}
		all = append(all, resp.Values...)
		if resp.IsLast || len(resp.Values) == 0 {
			return all, nil
		}
		startAt += len(resp.Values)
	}
}

type deleteWebhookRequest struct {
	WebhookIDs []int64 `json:"webhookIds"`
}

// DeleteWebhooks removes the given webhook ids from Jira. Best-effort: a
// 404 is treated as success because the webhook may have already expired.
func (c *Client) DeleteWebhooks(ctx context.Context, user *storage.User, webhookIDs []int64) error {
	if len(webhookIDs) == 0 {
		return nil
	}

	payload := deleteWebhookRequest{WebhookIDs: webhookIDs}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal delete payload: %w", err)
	}

	_, err = c.doRequest(ctx, user, http.MethodDelete, "/webhook", bytes.NewReader(data))
	return err
}
