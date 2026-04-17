package storage

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

const MaxTemplatesPerUser = 20

type IssueTemplate struct {
	ID             bson.ObjectID     `bson:"_id,omitempty"`
	TelegramUserID int64             `bson:"telegram_user_id"`
	Name           string            `bson:"name"`
	ProjectKey     string            `bson:"project_key"`
	IssueTypeID    string            `bson:"issue_type_id"`
	IssueTypeName  string            `bson:"issue_type_name"`
	Fields         map[string]string `bson:"fields"`
	CreatedTS      int64             `bson:"created_ts"`
	ModifiedTS     int64             `bson:"modified_ts"`
}

type TemplateRepo struct {
	coll *mongo.Collection
}

func NewTemplateRepo(db *mongo.Database) *TemplateRepo {
	return &TemplateRepo{coll: db.Collection("issue_templates")}
}

func (r *TemplateRepo) Create(ctx context.Context, tmpl *IssueTemplate) error {
	now := time.Now().Unix()
	tmpl.CreatedTS = now
	tmpl.ModifiedTS = now

	_, err := r.coll.InsertOne(ctx, tmpl)
	return err
}

func (r *TemplateRepo) GetByUser(ctx context.Context, telegramUserID int64) ([]IssueTemplate, error) {
	cursor, err := r.coll.Find(ctx, bson.M{"telegram_user_id": telegramUserID})
	if err != nil {
		return nil, err
	}

	var templates []IssueTemplate
	if err := cursor.All(ctx, &templates); err != nil {
		return nil, err
	}

	return templates, nil
}

func (r *TemplateRepo) GetByID(ctx context.Context, id bson.ObjectID) (*IssueTemplate, error) {
	var tmpl IssueTemplate
	err := r.coll.FindOne(ctx, bson.M{"_id": id}).Decode(&tmpl)
	if err != nil {
		return nil, err
	}
	return &tmpl, nil
}

func (r *TemplateRepo) CountByUser(ctx context.Context, telegramUserID int64) (int64, error) {
	return r.coll.CountDocuments(ctx, bson.M{"telegram_user_id": telegramUserID})
}

func (r *TemplateRepo) Delete(ctx context.Context, id bson.ObjectID) error {
	_, err := r.coll.DeleteOne(ctx, bson.M{"_id": id})
	return err
}

func (r *TemplateRepo) DeleteByUserID(ctx context.Context, userID int64) error {
	_, err := r.coll.DeleteMany(ctx, bson.M{"telegram_user_id": userID})
	return err
}
