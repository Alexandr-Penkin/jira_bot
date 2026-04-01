package storage

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type ScheduledReport struct {
	ID             bson.ObjectID `bson:"_id,omitempty"`
	TelegramChatID int64         `bson:"telegram_chat_id"`
	TelegramUserID int64         `bson:"telegram_user_id"`
	CronExpression string        `bson:"cron_expression"`
	JQL            string        `bson:"jql"`
	ReportName     string        `bson:"report_name"`
	IsActive       bool          `bson:"is_active"`
	CreatedTS      int64         `bson:"created_ts"`
	ModifiedTS     int64         `bson:"modified_ts"`
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
