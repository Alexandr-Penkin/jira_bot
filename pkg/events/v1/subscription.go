package eventsv1

import "strconv"

// SubscriptionCreated fires after a new Subscription row is persisted.
type SubscriptionCreated struct {
	SubscriptionID   string `json:"subscription_id"`
	TelegramID       int64  `json:"telegram_id"`
	ChatID           int64  `json:"chat_id"`
	SubscriptionType string `json:"subscription_type"`
	ProjectKey       string `json:"project_key,omitempty"`
	IssueKey         string `json:"issue_key,omitempty"`
	FilterID         string `json:"filter_id,omitempty"`
	FilterName       string `json:"filter_name,omitempty"`
	FilterJQL        string `json:"filter_jql,omitempty"`
	At               int64  `json:"at"`
}

func (*SubscriptionCreated) Subject() string { return SubjectSubscriptionCreated }

func (e *SubscriptionCreated) IdempotencyKey() string {
	return "subscription.created:" + e.SubscriptionID
}

// SubscriptionDeleted fires after a subscription is removed.
type SubscriptionDeleted struct {
	SubscriptionID string `json:"subscription_id"`
	TelegramID     int64  `json:"telegram_id"`
	ChatID         int64  `json:"chat_id"`
	At             int64  `json:"at"`
}

func (SubscriptionDeleted) Subject() string { return SubjectSubscriptionDeleted }

func (e SubscriptionDeleted) IdempotencyKey() string {
	return "subscription.deleted:" + e.SubscriptionID + ":" + strconv.FormatInt(e.At, 10)
}

// ChangeDetected fires when the poller sees a new or updated issue matching
// a subscription. One event per (subscription, issue, change) triple.
type ChangeDetected struct {
	SubscriptionID   string `json:"subscription_id"`
	SubscriptionType string `json:"subscription_type"`
	TelegramID       int64  `json:"telegram_id"`
	ChatID           int64  `json:"chat_id"`
	IssueKey         string `json:"issue_key"`
	ChangeType       string `json:"change_type"`
	Actor            string `json:"actor,omitempty"`
	DetectedAt       int64  `json:"detected_at"`
}

func (ChangeDetected) Subject() string { return SubjectChangeDetected }

func (e ChangeDetected) IdempotencyKey() string {
	return "subscription.change_detected:" + e.SubscriptionID + ":" + e.IssueKey +
		":" + e.ChangeType + ":" + strconv.FormatInt(e.DetectedAt, 10)
}
