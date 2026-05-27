package storage

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"SleepJiraBot/internal/crypto"
)

// OAuthPendingDoc is the persistent form of a multi-site OAuth selection
// awaiting the user's pick. Stored in Mongo so the process that received
// the OAuth callback (cmd/bot) and the process that handles the Telegram
// site-selection button (telegram-svc) share the same state — without
// this, the in-memory map is invisible cross-process and selection fails.
//
// Payload is the encrypted JSON of the pending selection (it carries the
// freshly-issued access/refresh tokens, so it is never stored in clear).
type OAuthPendingDoc struct {
	TelegramUserID int64     `bson:"_id"`
	Payload        string    `bson:"payload"`
	CreatedAt      time.Time `bson:"created_at"`
}

// OAuthPendingRepo stores at most one pending site selection per Telegram
// user. It deals only in opaque (already-meaningful-to-the-caller) JSON
// strings which it encrypts at rest, so storage stays free of any jira
// type dependency.
type OAuthPendingRepo struct {
	coll *mongo.Collection
	enc  *crypto.Encryptor
}

// ErrOAuthPendingNotFound is returned by Consume when there is no pending
// selection for the user (never created, already consumed, or expired).
var ErrOAuthPendingNotFound = errors.New("oauth pending selection not found")

func NewOAuthPendingRepo(db *mongo.Database, enc *crypto.Encryptor) *OAuthPendingRepo {
	return &OAuthPendingRepo{coll: db.Collection("oauth_pending_sites"), enc: enc}
}

// Save upserts the pending selection for a user, encrypting the payload.
// The caller-supplied createdAt drives the TTL window and the consumer's
// age check.
func (r *OAuthPendingRepo) Save(ctx context.Context, telegramUserID int64, payload string, createdAt time.Time) error {
	ciphertext, err := r.enc.Encrypt(payload)
	if err != nil {
		return err
	}
	_, err = r.coll.ReplaceOne(
		ctx,
		bson.M{"_id": telegramUserID},
		OAuthPendingDoc{
			TelegramUserID: telegramUserID,
			Payload:        ciphertext,
			CreatedAt:      createdAt,
		},
		options.Replace().SetUpsert(true),
	)
	return err
}

// Consume atomically reads and deletes the pending selection, returning
// the decrypted payload and its creation time. Returns
// ErrOAuthPendingNotFound when nothing is stored so the caller can show a
// "selection expired" message rather than a generic error.
func (r *OAuthPendingRepo) Consume(ctx context.Context, telegramUserID int64) (string, time.Time, error) {
	var doc OAuthPendingDoc
	err := r.coll.FindOneAndDelete(ctx, bson.M{"_id": telegramUserID}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return "", time.Time{}, ErrOAuthPendingNotFound
	}
	if err != nil {
		return "", time.Time{}, err
	}
	payload, err := r.enc.Decrypt(doc.Payload)
	if err != nil {
		return "", time.Time{}, err
	}
	return payload, doc.CreatedAt, nil
}

// EnsureIndexes wires a TTL index on created_at so Mongo prunes abandoned
// selections without the caller polling. The TTL (15m) is wider than the
// callback's own age check (pendingSiteMaxAge = 10m) so the application
// remains the authority on freshness; Mongo only sweeps stragglers.
func (r *OAuthPendingRepo) EnsureIndexes(ctx context.Context) error {
	_, err := r.coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "created_at", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(int32((15 * time.Minute).Seconds())),
	})
	return err
}
