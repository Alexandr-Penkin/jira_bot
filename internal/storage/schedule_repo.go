package storage

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Schedule kinds. "jql" is the legacy user-authored report (custom cron +
// JQL). "daily" is a recurring daily standup: cron fires once per day and
// the scheduler regenerates the three-block daily text from the user's
// saved daily JQLs (or their defaults) at send time.
const (
	ScheduleKindJQL   = "jql"
	ScheduleKindDaily = "daily"
)

type ScheduledReport struct {
	ID             bson.ObjectID `bson:"_id,omitempty"`
	TelegramChatID int64         `bson:"telegram_chat_id"`
	TelegramUserID int64         `bson:"telegram_user_id"`
	CronExpression string        `bson:"cron_expression"`
	JQL            string        `bson:"jql,omitempty"`
	ReportName     string        `bson:"report_name"`
	// Kind drives the scheduler's dispatch path. Empty on legacy docs
	// means the JQL path (see ScheduleKindJQL).
	Kind string `bson:"kind,omitempty"`
	// Timezone is an IANA name (e.g. "Europe/Moscow"). Empty means the
	// cron expression's embedded CRON_TZ= takes over, or server local.
	// Stored separately so we can render it back to the user without
	// re-parsing the cron string.
	Timezone   string `bson:"timezone,omitempty"`
	IsActive   bool   `bson:"is_active"`
	CreatedTS  int64  `bson:"created_ts"`
	ModifiedTS int64  `bson:"modified_ts"`
}

type ScheduleRepo struct {
	coll *mongo.Collection
}

func NewScheduleRepo(db *mongo.Database) *ScheduleRepo {
	return &ScheduleRepo{coll: db.Collection("scheduled_reports")}
}

func (r *ScheduleRepo) Create(ctx context.Context, report *ScheduledReport) error {
	now := time.Now().Unix()
	report.CreatedTS = now
	report.ModifiedTS = now
	report.IsActive = true

	_, err := r.coll.InsertOne(ctx, report)
	return err
}

func (r *ScheduleRepo) GetAllActive(ctx context.Context) ([]ScheduledReport, error) {
	cursor, err := r.coll.Find(ctx, bson.M{"is_active": true})
	if err != nil {
		return nil, err
	}

	var reports []ScheduledReport
	if err := cursor.All(ctx, &reports); err != nil {
		return nil, err
	}

	return reports, nil
}

func (r *ScheduleRepo) GetByChat(ctx context.Context, chatID int64) ([]ScheduledReport, error) {
	cursor, err := r.coll.Find(ctx, bson.M{
		"telegram_chat_id": chatID,
		"is_active":        true,
	})
	if err != nil {
		return nil, err
	}

	var reports []ScheduledReport
	if err := cursor.All(ctx, &reports); err != nil {
		return nil, err
	}

	return reports, nil
}

// CountActive returns the total number of active schedules.
func (r *ScheduleRepo) CountActive(ctx context.Context) (int64, error) {
	return r.coll.CountDocuments(ctx, bson.M{"is_active": true})
}

func (r *ScheduleRepo) Delete(ctx context.Context, id bson.ObjectID) error {
	_, err := r.coll.DeleteOne(ctx, bson.M{"_id": id})
	return err
}

func (r *ScheduleRepo) DeleteByChat(ctx context.Context, chatID int64) error {
	_, err := r.coll.DeleteMany(ctx, bson.M{"telegram_chat_id": chatID})
	return err
}

func (r *ScheduleRepo) DeleteByUserID(ctx context.Context, userID int64) error {
	_, err := r.coll.DeleteMany(ctx, bson.M{"telegram_user_id": userID})
	return err
}

// GetDaily returns the single daily subscription for (chat, user), or nil
// if none exists. There is at most one per pair by construction (see
// UpsertDaily).
func (r *ScheduleRepo) GetDaily(ctx context.Context, chatID, userID int64) (*ScheduledReport, error) {
	var report ScheduledReport
	err := r.coll.FindOne(ctx, bson.M{
		"telegram_chat_id": chatID,
		"telegram_user_id": userID,
		"kind":             ScheduleKindDaily,
	}).Decode(&report)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &report, nil
}

// UpsertDaily replaces any existing daily subscription for (chat, user)
// with the given one. Ensures the single-subscription-per-chat invariant
// even if the user clicks through the time picker repeatedly.
func (r *ScheduleRepo) UpsertDaily(ctx context.Context, report *ScheduledReport) error {
	if report.Kind == "" {
		report.Kind = ScheduleKindDaily
	}
	now := time.Now().Unix()
	filter := bson.M{
		"telegram_chat_id": report.TelegramChatID,
		"telegram_user_id": report.TelegramUserID,
		"kind":             ScheduleKindDaily,
	}
	update := bson.M{
		"$set": bson.M{
			"cron_expression": report.CronExpression,
			"timezone":        report.Timezone,
			"report_name":     report.ReportName,
			"is_active":       true,
			"modified_ts":     now,
		},
		"$setOnInsert": bson.M{
			"created_ts": now,
		},
	}
	opts := options.UpdateOne().SetUpsert(true)
	_, err := r.coll.UpdateOne(ctx, filter, update, opts)
	return err
}

// DeleteDaily removes the daily subscription for (chat, user). No-op if
// none exists.
func (r *ScheduleRepo) DeleteDaily(ctx context.Context, chatID, userID int64) error {
	_, err := r.coll.DeleteOne(ctx, bson.M{
		"telegram_chat_id": chatID,
		"telegram_user_id": userID,
		"kind":             ScheduleKindDaily,
	})
	return err
}
