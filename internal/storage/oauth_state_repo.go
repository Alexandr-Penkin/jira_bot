package storage

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// OAuthStateDoc is the persistent form of an OAuth state token. Stored in
// Mongo so multiple processes (cmd/bot's CallbackServer and cmd/telegram-svc's
// /connect handler) can share the same state lifecycle — without this, a
// state generated in one process is invisible to the other and the OAuth
// callback rejects every login with "invalid or expired state".
type OAuthStateDoc struct {
	State          string    `bson:"_id"`
	TelegramUserID int64     `bson:"telegram_user_id"`
	CreatedAt      time.Time `bson:"created_at"`
}

type OAuthStateRepo struct {
	coll *mongo.Collection
}

// ErrOAuthStateNotFound is returned by Consume when the state token does
// not exist (already consumed, expired and pruned, or never created).
var ErrOAuthStateNotFound = errors.New("oauth state not found")

func NewOAuthStateRepo(db *mongo.Database) *OAuthStateRepo {
	return &OAuthStateRepo{coll: db.Collection("oauth_states")}
}

// Save inserts a fresh state token. The caller-supplied createdAt drives
// the TTL window — consumers verify the age before honouring a state.
func (r *OAuthStateRepo) Save(ctx context.Context, state string, telegramUserID int64, createdAt time.Time) error {
	_, err := r.coll.InsertOne(ctx, OAuthStateDoc{
		State:          state,
		TelegramUserID: telegramUserID,
		CreatedAt:      createdAt,
	})
	return err
}

// Consume atomically reads and deletes a state token. Returns
// ErrOAuthStateNotFound when the state does not exist so the caller can
// distinguish "unknown state" from a Mongo error.
func (r *OAuthStateRepo) Consume(ctx context.Context, state string) (OAuthStateDoc, error) {
	var doc OAuthStateDoc
	err := r.coll.FindOneAndDelete(ctx, bson.M{"_id": state}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return OAuthStateDoc{}, ErrOAuthStateNotFound
	}
	return doc, err
}

// DeleteExpired removes state docs older than the given cutoff. Called
// periodically by the OAuth client's cleanup goroutine so abandoned auth
// flows do not pile up forever.
func (r *OAuthStateRepo) DeleteExpired(ctx context.Context, olderThan time.Time) error {
	_, err := r.coll.DeleteMany(ctx, bson.M{"created_at": bson.M{"$lt": olderThan}})
	return err
}

// EnsureIndexes wires a TTL index on created_at so Mongo prunes
// abandoned states without the client having to poll. The TTL is wide
// enough to give the OAuth client itself a chance to consume first.
func (r *OAuthStateRepo) EnsureIndexes(ctx context.Context) error {
	_, err := r.coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "created_at", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(int32((30 * time.Minute).Seconds())),
	})
	return err
}
