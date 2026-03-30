package storage

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

type MongoDB struct {
	client *mongo.Client
	db     *mongo.Database
	log    zerolog.Logger
}

func ConnectMongo(ctx context.Context, uri, dbName string, log zerolog.Logger) (*MongoDB, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
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

func ensureIndexes(ctx context.Context, db *mongo.Database, log zerolog.Logger) error {
	indexes := []struct {
		collection string
		model      mongo.IndexModel
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
			collection: "subscriptions",
			model: mongo.IndexModel{
				Keys: bson.D{
					{Key: "is_active", Value: 1},
					{Key: "event_types", Value: 1},
					{Key: "jira_project_key", Value: 1},
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
	}

	for _, idx := range indexes {
		_, err := db.Collection(idx.collection).Indexes().CreateOne(ctx, idx.model)
		if err != nil {
			return fmt.Errorf("create index on %s: %w", idx.collection, err)
		}
	}

	log.Info().Msg("MongoDB indexes ensured")
	return nil
}

func (m *MongoDB) Database() *mongo.Database {
	return m.db
}

func (m *MongoDB) Disconnect(ctx context.Context) error {
	m.log.Info().Msg("disconnecting from MongoDB")
	return m.client.Disconnect(ctx)
}
