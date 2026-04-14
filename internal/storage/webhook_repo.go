package storage

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// WebhookRegistration tracks a dynamic webhook registered with Jira Cloud
// via POST /rest/api/3/webhook on behalf of a specific user/site. Atlassian
// expires these after 30 days unless refreshed via PUT /webhook/refresh,
// so we persist the expiry date and have a background job extend them.
type WebhookRegistration struct {
	ID             bson.ObjectID `bson:"_id,omitempty"`
	TelegramUserID int64         `bson:"telegram_user_id"`
	JiraCloudID    string        `bson:"jira_cloud_id"`
	// SubscriptionID is the Mongo _id of the subscription that owns this
	// webhook. When the subscription is deleted, this webhook is deleted
	// from Jira too.
	SubscriptionID bson.ObjectID `bson:"subscription_id"`
	// WebhookID is the numeric id Jira returned in the registration
	// response (`createdWebhookId`). Used for refresh and delete calls.
	WebhookID int64 `bson:"webhook_id"`
	// JqlFilter is the JQL we registered. Stored so we can re-register
	// after a failed refresh or after a /disconnect → /connect cycle.
	JqlFilter string `bson:"jql_filter"`
	// Events is the list of Jira webhook event names we subscribed to.
	Events []string `bson:"events"`
	// ExpiresAt is when Jira will drop the webhook unless refreshed.
	ExpiresAt  time.Time `bson:"expires_at"`
	CreatedTS  int64     `bson:"created_ts"`
	ModifiedTS int64     `bson:"modified_ts"`
}

type WebhookRepo struct {
	coll *mongo.Collection
}

func NewWebhookRepo(db *mongo.Database) *WebhookRepo {
	return &WebhookRepo{coll: db.Collection("webhook_registrations")}
}

func (r *WebhookRepo) Create(ctx context.Context, reg *WebhookRegistration) error {
	now := time.Now().Unix()
	reg.CreatedTS = now
	reg.ModifiedTS = now
	_, err := r.coll.InsertOne(ctx, reg)
	return err
}

// UpdateExpiry updates the expires_at field after a successful refresh.
func (r *WebhookRepo) UpdateExpiry(ctx context.Context, id bson.ObjectID, expiresAt time.Time) error {
	_, err := r.coll.UpdateByID(ctx, id, bson.M{
		"$set": bson.M{
			"expires_at":  expiresAt,
			"modified_ts": time.Now().Unix(),
		},
	})
	return err
}

func (r *WebhookRepo) GetByUser(ctx context.Context, telegramUserID int64) ([]WebhookRegistration, error) {
	cursor, err := r.coll.Find(ctx, bson.M{"telegram_user_id": telegramUserID})
	if err != nil {
		return nil, err
	}
	var regs []WebhookRegistration
	if err := cursor.All(ctx, &regs); err != nil {
		return nil, err
	}
	return regs, nil
}

func (r *WebhookRepo) GetBySubscription(ctx context.Context, subscriptionID bson.ObjectID) ([]WebhookRegistration, error) {
	cursor, err := r.coll.Find(ctx, bson.M{"subscription_id": subscriptionID})
	if err != nil {
		return nil, err
	}
	var regs []WebhookRegistration
	if err := cursor.All(ctx, &regs); err != nil {
		return nil, err
	}
	return regs, nil
}

// GetExpiringBefore returns webhook registrations whose expiry is before the
// given threshold. Used by the background refresher.
func (r *WebhookRepo) GetExpiringBefore(ctx context.Context, threshold time.Time) ([]WebhookRegistration, error) {
	cursor, err := r.coll.Find(ctx, bson.M{
		"expires_at": bson.M{"$lt": threshold},
	})
	if err != nil {
		return nil, err
	}
	var regs []WebhookRegistration
	if err := cursor.All(ctx, &regs); err != nil {
		return nil, err
	}
	return regs, nil
}

// CountAll returns the total number of webhook registrations.
func (r *WebhookRepo) CountAll(ctx context.Context) (int64, error) {
	return r.coll.CountDocuments(ctx, bson.M{})
}

func (r *WebhookRepo) Delete(ctx context.Context, id bson.ObjectID) error {
	_, err := r.coll.DeleteOne(ctx, bson.M{"_id": id})
	return err
}

func (r *WebhookRepo) DeleteBySubscription(ctx context.Context, subscriptionID bson.ObjectID) error {
	_, err := r.coll.DeleteMany(ctx, bson.M{"subscription_id": subscriptionID})
	return err
}

func (r *WebhookRepo) DeleteByUser(ctx context.Context, telegramUserID int64) error {
	_, err := r.coll.DeleteMany(ctx, bson.M{"telegram_user_id": telegramUserID})
	return err
}
