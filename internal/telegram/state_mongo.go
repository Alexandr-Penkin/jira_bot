package telegram

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	stateMongoOpTimeout = 500 * time.Millisecond
	stateCollectionName = "conversation_states"
)

type stateDoc struct {
	ChatID    int64             `bson:"chat_id"`
	UserID    int64             `bson:"user_id"`
	Step      string            `bson:"step"`
	Data      map[string]string `bson:"data,omitempty"`
	UpdatedAt time.Time         `bson:"updated_at"`
}

// mongoStateStore persists FSM state in Mongo so conversation progress
// survives process restarts and — once telegram-svc scales beyond one
// replica — cross-instance. A TTL index on updated_at handles expiry;
// there is no manual cleanup goroutine.
type mongoStateStore struct {
	coll *mongo.Collection
	log  zerolog.Logger
}

// NewMongoStateStore wires a Mongo-backed FSM store and ensures the
// TTL + uniqueness indexes. Callers should call this before NewBot.
//
// Migration: before this change the unique index was on user_id alone,
// which collapsed group-chat conversations across users. We drop the
// stale index (best-effort, error swallowed) before creating the new
// compound one — Mongo refuses CreateOne if a conflicting index exists.
func NewMongoStateStore(ctx context.Context, db *mongo.Database, log zerolog.Logger) (stateStore, error) {
	if db == nil {
		return nil, errors.New("telegram: mongo database required for conversation_states")
	}
	coll := db.Collection(stateCollectionName)

	idxCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Drop the pre-(chat_id,user_id) unique index if it exists. Older
	// builds named it after the single key (`user_id_1`); newer drivers
	// occasionally rename. We tolerate "not found" silently because
	// fresh installs never had it.
	if err := coll.Indexes().DropOne(idxCtx, "user_id_1"); err != nil {
		// IndexNotFound is the expected path on fresh installs; only
		// log unexpected errors so an operator notices real failures.
		var cmdErr mongo.CommandError
		if !errors.As(err, &cmdErr) || cmdErr.Code != 27 { // 27 = IndexNotFound
			log.Debug().Err(err).Msg("telegram: pre-existing user_id_1 index drop returned non-fatal error; continuing")
		}
	}

	// Unique compound index on (chat_id, user_id) for upsert-by-pair
	// semantics. Group chats with several concurrent users keep their
	// FSMs separate; private chats (chatID==userID) collapse cleanly.
	if _, err := coll.Indexes().CreateOne(idxCtx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "chat_id", Value: 1},
			{Key: "user_id", Value: 1},
		},
		Options: options.Index().SetUnique(true),
	}); err != nil {
		return nil, err
	}

	// TTL index: Mongo expires documents stateMaxAge after updated_at.
	if _, err := coll.Indexes().CreateOne(idxCtx, mongo.IndexModel{
		Keys:    bson.D{{Key: "updated_at", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(int32(stateMaxAge.Seconds())),
	}); err != nil {
		return nil, err
	}

	return &mongoStateStore{coll: coll, log: log}, nil
}

func (s *mongoStateStore) StartCleanup(_ context.Context) {
	// No-op: the TTL index handles expiry on the server side.
}

func (s *mongoStateStore) Set(chatID, userID int64, step string, data map[string]string) {
	ctx, cancel := context.WithTimeout(context.Background(), stateMongoOpTimeout)
	defer cancel()

	doc := stateDoc{ChatID: chatID, UserID: userID, Step: step, Data: data, UpdatedAt: time.Now()}
	opts := options.UpdateOne().SetUpsert(true)
	if _, err := s.coll.UpdateOne(ctx, bson.M{"chat_id": chatID, "user_id": userID}, bson.M{"$set": doc}, opts); err != nil {
		s.log.Warn().Err(err).Int64("chat_id", chatID).Int64("user_id", userID).Msg("telegram: mongo state Set failed")
	}
}

func (s *mongoStateStore) Get(chatID, userID int64) (step string, data map[string]string) {
	ctx, cancel := context.WithTimeout(context.Background(), stateMongoOpTimeout)
	defer cancel()

	var doc stateDoc
	err := s.coll.FindOne(ctx, bson.M{"chat_id": chatID, "user_id": userID}).Decode(&doc)
	if err != nil {
		if !errors.Is(err, mongo.ErrNoDocuments) {
			s.log.Warn().Err(err).Int64("chat_id", chatID).Int64("user_id", userID).Msg("telegram: mongo state Get failed")
		}
		return "", nil
	}
	// Defensive: TTL index can lag the in-code cutoff by up to 60s
	// (Mongo expiry monitor interval); mirror the memory-store rule.
	if time.Since(doc.UpdatedAt) > stateMaxAge {
		return "", nil
	}
	return doc.Step, doc.Data
}

func (s *mongoStateStore) Clear(chatID, userID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), stateMongoOpTimeout)
	defer cancel()

	if _, err := s.coll.DeleteOne(ctx, bson.M{"chat_id": chatID, "user_id": userID}); err != nil {
		s.log.Warn().Err(err).Int64("chat_id", chatID).Int64("user_id", userID).Msg("telegram: mongo state Clear failed")
	}
}
