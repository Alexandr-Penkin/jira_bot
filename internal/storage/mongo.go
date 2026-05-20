package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"

	"SleepJiraBot/pkg/telemetry"
)

type MongoDB struct {
	client *mongo.Client
	db     *mongo.Database
	log    zerolog.Logger
}

func ConnectMongo(ctx context.Context, uri, dbName string, log zerolog.Logger) (*MongoDB, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetMonitor(telemetry.MongoCommandMonitor()))
	if err != nil {
		return nil, fmt.Errorf("mongo connect: %w", err)
	}

	if err = client.Ping(ctx, readpref.Primary()); err != nil {
		_ = client.Disconnect(ctx)
		return nil, fmt.Errorf("mongo ping: %w", err)
	}

	log.Info().Str("db", dbName).Msg("connected to MongoDB")

	db := client.Database(dbName)

	if err = ensureIndexes(ctx, db, log); err != nil {
		_ = client.Disconnect(ctx)
		return nil, fmt.Errorf("ensure indexes: %w", err)
	}

	return &MongoDB{
		client: client,
		db:     db,
		log:    log,
	}, nil
}

// Mongo error codes we recognise during index migration.
//
//	IndexOptionsConflict (85): same name + key but different options
//	                           (e.g. an existing non-TTL index where we
//	                           now want SetExpireAfterSeconds).
//	IndexKeySpecsConflict (86): same name but different key spec.
//
// On either, we drop the existing index and recreate it with the new
// options. recreateOnConflict marks indexes where this is safe — the
// data itself doesn't depend on the index, so a brief gap during
// migration is fine.
const (
	mongoErrIndexOptionsConflict  = 85
	mongoErrIndexKeySpecsConflict = 86
)

func ensureIndexes(ctx context.Context, db *mongo.Database, log zerolog.Logger) error {
	indexes := []struct {
		collection         string
		model              mongo.IndexModel
		recreateOnConflict bool
		dropName           string // name used when force-dropping
	}{
		{
			collection: "users",
			model: mongo.IndexModel{
				Keys:    bson.D{{Key: "telegram_user_id", Value: 1}},
				Options: options.Index().SetUnique(true),
			},
		},
		{
			collection: "subscriptions",
			model: mongo.IndexModel{
				Keys: bson.D{
					{Key: "telegram_chat_id", Value: 1},
					{Key: "is_active", Value: 1},
				},
			},
		},
		{
			// Hot path: webhook handler fan-out by project key.
			collection: "subscriptions",
			model: mongo.IndexModel{
				Keys: bson.D{
					{Key: "is_active", Value: 1},
					{Key: "subscription_type", Value: 1},
					{Key: "jira_project_key", Value: 1},
				},
			},
		},
		{
			// Hot path: webhook handler fan-out by issue key.
			collection: "subscriptions",
			model: mongo.IndexModel{
				Keys: bson.D{
					{Key: "is_active", Value: 1},
					{Key: "subscription_type", Value: 1},
					{Key: "jira_issue_key", Value: 1},
				},
			},
		},
		{
			// Hot path: poller GetActiveByUser + mention lookup by user IDs.
			collection: "subscriptions",
			model: mongo.IndexModel{
				Keys: bson.D{
					{Key: "is_active", Value: 1},
					{Key: "telegram_user_id", Value: 1},
				},
			},
		},
		{
			collection: "scheduled_reports",
			model: mongo.IndexModel{
				Keys: bson.D{
					{Key: "is_active", Value: 1},
				},
			},
		},
		{
			collection: "scheduled_reports",
			model: mongo.IndexModel{
				Keys: bson.D{
					{Key: "telegram_chat_id", Value: 1},
					{Key: "is_active", Value: 1},
				},
			},
		},
		{
			collection: "issue_templates",
			model: mongo.IndexModel{
				Keys: bson.D{{Key: "telegram_user_id", Value: 1}},
			},
		},
		{
			collection: "webhook_registrations",
			model: mongo.IndexModel{
				Keys: bson.D{{Key: "telegram_user_id", Value: 1}},
			},
		},
		{
			collection: "webhook_registrations",
			model: mongo.IndexModel{
				Keys: bson.D{{Key: "subscription_id", Value: 1}},
			},
		},
		{
			// TTL: Mongo prunes the doc once expires_at is in the past.
			// Webhooks are recreated by the refresher before this fires
			// in the happy path; the index guarantees abandoned rows
			// (user disconnected, refresher offline for days) clean up
			// instead of accumulating forever.
			//
			// recreateOnConflict: older deploys created a plain index
			// here without TTL. Mongo rejects CreateOne when the same
			// name + key already exists with different options
			// (IndexOptionsConflict). Drop the old one and recreate so
			// upgrades don't have to be ordered with manual migrations.
			collection: "webhook_registrations",
			model: mongo.IndexModel{
				Keys:    bson.D{{Key: "expires_at", Value: 1}},
				Options: options.Index().SetExpireAfterSeconds(0),
			},
			recreateOnConflict: true,
			dropName:           "expires_at_1",
		},
	}

	for _, idx := range indexes {
		coll := db.Collection(idx.collection)
		_, err := coll.Indexes().CreateOne(ctx, idx.model)
		if err == nil {
			continue
		}
		if idx.recreateOnConflict && isIndexConflict(err) {
			log.Warn().
				Str("collection", idx.collection).
				Str("name", idx.dropName).
				Msg("MongoDB: existing index conflicts with new options; dropping and recreating")
			if dropErr := coll.Indexes().DropOne(ctx, idx.dropName); dropErr != nil {
				return fmt.Errorf("recreate index on %s: drop %s: %w", idx.collection, idx.dropName, dropErr)
			}
			if _, retryErr := coll.Indexes().CreateOne(ctx, idx.model); retryErr != nil {
				return fmt.Errorf("recreate index on %s: %w", idx.collection, retryErr)
			}
			continue
		}
		return fmt.Errorf("create index on %s: %w", idx.collection, err)
	}

	log.Info().Msg("MongoDB indexes ensured")
	return nil
}

// isIndexConflict reports whether err is one of the Mongo conflict
// codes we know how to repair by drop+recreate.
func isIndexConflict(err error) bool {
	var cmdErr mongo.CommandError
	if !errors.As(err, &cmdErr) {
		return false
	}
	return cmdErr.Code == mongoErrIndexOptionsConflict || cmdErr.Code == mongoErrIndexKeySpecsConflict
}

func (m *MongoDB) Database() *mongo.Database {
	return m.db
}

// Ping issues a primary-readPreference ping. Used by readiness probes so
// a *-svc pod whose Mongo lost the primary stops receiving traffic.
func (m *MongoDB) Ping(ctx context.Context) error {
	return m.client.Ping(ctx, readpref.Primary())
}

func (m *MongoDB) Disconnect(ctx context.Context) error {
	m.log.Info().Msg("disconnecting from MongoDB")
	return m.client.Disconnect(ctx)
}
