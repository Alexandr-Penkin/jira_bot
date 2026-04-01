package storage

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
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
}

func NewSubscriptionRepo(db *mongo.Database) *SubscriptionRepo {
	return &SubscriptionRepo{coll: db.Collection("subscriptions")}
}

func (r *SubscriptionRepo) Create(ctx context.Context, sub *Subscription) error {
	now := time.Now().Unix()
	sub.CreatedTS = now
	sub.ModifiedTS = now
	sub.IsActive = true

	_, err := r.coll.InsertOne(ctx, sub)
	return err
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
	_, err := r.coll.DeleteOne(ctx, bson.M{"_id": id})
	return err
}

func (r *SubscriptionRepo) DeleteByChat(ctx context.Context, chatID int64) error {
	_, err := r.coll.DeleteMany(ctx, bson.M{"telegram_chat_id": chatID})
	return err
}

func (r *SubscriptionRepo) DeleteByUserID(ctx context.Context, userID int64) error {
	_, err := r.coll.DeleteMany(ctx, bson.M{"telegram_user_id": userID})
	return err
}
