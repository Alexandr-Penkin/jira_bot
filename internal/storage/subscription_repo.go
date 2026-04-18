package storage

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	eventsv1 "SleepJiraBot/pkg/events/v1"
)

const (
	SubTypeMyNewIssues    = "my_new_issues"
	SubTypeMyMentions     = "my_mentions"
	SubTypeMyWatched      = "my_watched"
	SubTypeProjectUpdates = "project_updates"
	SubTypeIssueUpdates   = "issue_updates"
	SubTypeFilterUpdates  = "filter_updates"
)

type Subscription struct {
	ID               bson.ObjectID `bson:"_id,omitempty"`
	TelegramChatID   int64         `bson:"telegram_chat_id"`
	TelegramUserID   int64         `bson:"telegram_user_id"`
	SubscriptionType string        `bson:"subscription_type"`
	JiraProjectKey   string        `bson:"jira_project_key,omitempty"`
	JiraIssueKey     string        `bson:"jira_issue_key,omitempty"`
	JiraFilterID     string        `bson:"jira_filter_id,omitempty"`
	JiraFilterName   string        `bson:"jira_filter_name,omitempty"`
	JiraFilterJQL    string        `bson:"jira_filter_jql,omitempty"`
	IsActive         bool          `bson:"is_active"`
	LastPolledAt     int64         `bson:"last_polled_at,omitempty"`
	CreatedTS        int64         `bson:"created_ts"`
	ModifiedTS       int64         `bson:"modified_ts"`
}

type SubscriptionRepo struct {
	coll *mongo.Collection
	pub  eventsv1.Publisher
}

func NewSubscriptionRepo(db *mongo.Database) *SubscriptionRepo {
	return &SubscriptionRepo{
		coll: db.Collection("subscriptions"),
		pub:  eventsv1.NoopPublisher{},
	}
}

// SetEventPublisher installs a domain event publisher. Subscription create
// and delete operations emit SubscriptionCreated / SubscriptionDeleted on
// success when a non-nil publisher is installed.
func (r *SubscriptionRepo) SetEventPublisher(p eventsv1.Publisher) {
	if p == nil {
		r.pub = eventsv1.NoopPublisher{}
		return
	}
	r.pub = p
}

func (r *SubscriptionRepo) Create(ctx context.Context, sub *Subscription) error {
	now := time.Now().Unix()
	sub.CreatedTS = now
	sub.ModifiedTS = now
	sub.IsActive = true

	res, err := r.coll.InsertOne(ctx, sub)
	if err != nil {
		return err
	}
	if oid, ok := res.InsertedID.(bson.ObjectID); ok {
		sub.ID = oid
	}

	_ = r.pub.Publish(ctx, &eventsv1.SubscriptionCreated{
		SubscriptionID:   sub.ID.Hex(),
		TelegramID:       sub.TelegramUserID,
		ChatID:           sub.TelegramChatID,
		SubscriptionType: sub.SubscriptionType,
		ProjectKey:       sub.JiraProjectKey,
		IssueKey:         sub.JiraIssueKey,
		FilterID:         sub.JiraFilterID,
		FilterName:       sub.JiraFilterName,
		FilterJQL:        sub.JiraFilterJQL,
		At:               now,
	}, "")
	return nil
}

// Exists checks if a subscription of the given type already exists for the user/chat.
func (r *SubscriptionRepo) Exists(ctx context.Context, chatID int64, subType string, extra bson.M) (bool, error) {
	filter := bson.M{
		"telegram_chat_id":  chatID,
		"subscription_type": subType,
		"is_active":         true,
	}
	for k, v := range extra {
		filter[k] = v
	}
	count, err := r.coll.CountDocuments(ctx, filter)
	return count > 0, err
}

func (r *SubscriptionRepo) GetByChat(ctx context.Context, chatID int64) ([]Subscription, error) {
	cursor, err := r.coll.Find(ctx, bson.M{
		"telegram_chat_id": chatID,
		"is_active":        true,
	})
	if err != nil {
		return nil, err
	}

	var subs []Subscription
	if err := cursor.All(ctx, &subs); err != nil {
		return nil, err
	}

	return subs, nil
}

func (r *SubscriptionRepo) GetAllActive(ctx context.Context) ([]Subscription, error) {
	cursor, err := r.coll.Find(ctx, bson.M{"is_active": true})
	if err != nil {
		return nil, err
	}

	var subs []Subscription
	if err := cursor.All(ctx, &subs); err != nil {
		return nil, err
	}

	return subs, nil
}

// GetActiveByProjectKey returns active subscriptions matching the given project key.
func (r *SubscriptionRepo) GetActiveByProjectKey(ctx context.Context, projectKey string) ([]Subscription, error) {
	cursor, err := r.coll.Find(ctx, bson.M{
		"is_active":         true,
		"subscription_type": SubTypeProjectUpdates,
		"jira_project_key":  projectKey,
	})
	if err != nil {
		return nil, err
	}

	var subs []Subscription
	if err := cursor.All(ctx, &subs); err != nil {
		return nil, err
	}

	return subs, nil
}

// GetActiveByIssueKey returns active subscriptions matching the given issue key.
func (r *SubscriptionRepo) GetActiveByIssueKey(ctx context.Context, issueKey string) ([]Subscription, error) {
	cursor, err := r.coll.Find(ctx, bson.M{
		"is_active":         true,
		"subscription_type": SubTypeIssueUpdates,
		"jira_issue_key":    issueKey,
	})
	if err != nil {
		return nil, err
	}

	var subs []Subscription
	if err := cursor.All(ctx, &subs); err != nil {
		return nil, err
	}

	return subs, nil
}

// GetActiveByUser returns active subscriptions for a given Telegram user.
func (r *SubscriptionRepo) GetActiveByUser(ctx context.Context, telegramUserID int64) ([]Subscription, error) {
	cursor, err := r.coll.Find(ctx, bson.M{
		"is_active":        true,
		"telegram_user_id": telegramUserID,
	})
	if err != nil {
		return nil, err
	}

	var subs []Subscription
	if err := cursor.All(ctx, &subs); err != nil {
		return nil, err
	}

	return subs, nil
}

// GetActiveUserIDs returns distinct Telegram user IDs that have active subscriptions.
func (r *SubscriptionRepo) GetActiveUserIDs(ctx context.Context) ([]int64, error) {
	var userIDs []int64
	if err := r.coll.Distinct(ctx, "telegram_user_id", bson.M{"is_active": true}).Decode(&userIDs); err != nil {
		return nil, err
	}

	return userIDs, nil
}

// GetMentionSubscriptionsByUserIDs returns active my_mentions subscriptions
// for the given Telegram user IDs.
func (r *SubscriptionRepo) GetMentionSubscriptionsByUserIDs(ctx context.Context, userIDs []int64) ([]Subscription, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	cursor, err := r.coll.Find(ctx, bson.M{
		"is_active":         true,
		"subscription_type": SubTypeMyMentions,
		"telegram_user_id":  bson.M{"$in": userIDs},
	})
	if err != nil {
		return nil, err
	}

	var subs []Subscription
	if err := cursor.All(ctx, &subs); err != nil {
		return nil, err
	}

	return subs, nil
}

// CountActive returns the total number of active subscriptions.
func (r *SubscriptionRepo) CountActive(ctx context.Context) (int64, error) {
	return r.coll.CountDocuments(ctx, bson.M{"is_active": true})
}

func (r *SubscriptionRepo) UpdateLastPolled(ctx context.Context, id bson.ObjectID, ts int64) error {
	_, err := r.coll.UpdateByID(ctx, id, bson.M{
		"$set": bson.M{"last_polled_at": ts},
	})
	return err
}

func (r *SubscriptionRepo) Delete(ctx context.Context, id bson.ObjectID) error {
	var sub Subscription
	_ = r.coll.FindOne(ctx, bson.M{"_id": id}).Decode(&sub)

	if _, err := r.coll.DeleteOne(ctx, bson.M{"_id": id}); err != nil {
		return err
	}

	if !sub.ID.IsZero() {
		_ = r.pub.Publish(ctx, eventsv1.SubscriptionDeleted{
			SubscriptionID: sub.ID.Hex(),
			TelegramID:     sub.TelegramUserID,
			ChatID:         sub.TelegramChatID,
			At:             time.Now().Unix(),
		}, "")
	}
	return nil
}

func (r *SubscriptionRepo) DeleteByChat(ctx context.Context, chatID int64) error {
	return r.deleteManyAndPublish(ctx, bson.M{"telegram_chat_id": chatID})
}

func (r *SubscriptionRepo) DeleteByUserID(ctx context.Context, userID int64) error {
	return r.deleteManyAndPublish(ctx, bson.M{"telegram_user_id": userID})
}

// deleteManyAndPublish snapshots the matching subscriptions before the
// delete so a SubscriptionDeleted event can be emitted per removed row.
// The read+delete pair is not transactional, but for Phase 0 this is
// observational data and a minor window of missed events is acceptable.
func (r *SubscriptionRepo) deleteManyAndPublish(ctx context.Context, filter bson.M) error {
	cursor, err := r.coll.Find(ctx, filter)
	var toDelete []Subscription
	if err == nil {
		_ = cursor.All(ctx, &toDelete)
	}

	if _, err := r.coll.DeleteMany(ctx, filter); err != nil {
		return err
	}

	now := time.Now().Unix()
	for i := range toDelete {
		s := &toDelete[i]
		_ = r.pub.Publish(ctx, eventsv1.SubscriptionDeleted{
			SubscriptionID: s.ID.Hex(),
			TelegramID:     s.TelegramUserID,
			ChatID:         s.TelegramChatID,
			At:             now,
		}, "")
	}
	return nil
}
