package storage

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// CalendarEventState records the last known snapshot of a single
// calendar instance per user. The compound key (TelegramUserID, UID,
// InstanceTime) uniquely identifies a concrete occurrence so the
// poller can tell "new event" from "changed event" from "already
// notified" across restarts.
type CalendarEventState struct {
	ID             bson.ObjectID `bson:"_id,omitempty"`
	TelegramUserID int64         `bson:"telegram_user_id"`
	UID            string        `bson:"event_uid"`
	InstanceTime   int64         `bson:"instance_ts"`
	Summary        string        `bson:"summary,omitempty"`
	Start          int64         `bson:"start_ts"`
	End            int64         `bson:"end_ts,omitempty"`
	Location       string        `bson:"location,omitempty"`
	Sequence       int           `bson:"sequence,omitempty"`
	LastModified   int64         `bson:"last_modified,omitempty"`
	ReminderSent   bool          `bson:"reminder_sent,omitempty"`
	NotifiedNew    bool          `bson:"notified_new,omitempty"`
	SeenAt         int64         `bson:"seen_at"`
}

// CalendarEventStateChange tells the poller what kind of transition
// UpsertState performed. Callers use it to decide whether to emit a
// "new event", "changed event", or no notification.
type CalendarEventStateChange struct {
	Created bool
	Changed bool
	Prev    *CalendarEventState
}

// CalendarEventRepo is the minimum surface the calendar poller needs.
// Behind it sits a Mongo collection with a unique compound index and a
// TTL on seen_at so the document set self-prunes when a user removes
// their calendar.
type CalendarEventRepo interface {
	UpsertState(ctx context.Context, s *CalendarEventState) (CalendarEventStateChange, error)
	GetByUserAndInstance(ctx context.Context, uid int64, eventUID string, instanceTS int64) (*CalendarEventState, error)
	MarkReminderSent(ctx context.Context, uid int64, eventUID string, instanceTS int64) error
	MarkNotifiedNew(ctx context.Context, uid int64, eventUID string, instanceTS int64) error
	DeleteStaleByUser(ctx context.Context, uid int64, keepStartTSAfter int64) error
	DeleteAllByUser(ctx context.Context, uid int64) error
	EnsureIndexes(ctx context.Context) error
}

// MongoCalendarEventRepo is the Mongo-backed implementation.
type MongoCalendarEventRepo struct {
	coll *mongo.Collection
}

// NewCalendarEventRepo wires the calendar_events collection.
func NewCalendarEventRepo(db *mongo.Database) *MongoCalendarEventRepo {
	return &MongoCalendarEventRepo{coll: db.Collection("calendar_events")}
}

// EnsureIndexes creates the indexes the poller relies on. Idempotent.
func (r *MongoCalendarEventRepo) EnsureIndexes(ctx context.Context) error {
	uniqueOpts := options.Index().SetUnique(true).SetName("user_uid_instance")
	startOpts := options.Index().SetName("user_start_ts")
	// TTL on seen_at: 90 days lets a removed calendar self-prune
	// without an explicit DeleteAllByUser, while keeping a generous
	// window for transient outages where polling is paused.
	ttlOpts := options.Index().SetName("seen_at_ttl").SetExpireAfterSeconds(int32((90 * 24 * time.Hour).Seconds()))

	_, err := r.coll.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys: bson.D{
				{Key: "telegram_user_id", Value: 1},
				{Key: "event_uid", Value: 1},
				{Key: "instance_ts", Value: 1},
			},
			Options: uniqueOpts,
		},
		{
			Keys: bson.D{
				{Key: "telegram_user_id", Value: 1},
				{Key: "start_ts", Value: 1},
			},
			Options: startOpts,
		},
		{
			Keys:    bson.D{{Key: "seen_at", Value: 1}},
			Options: ttlOpts,
		},
	})
	return err
}

// UpsertState writes the new snapshot and reports whether the row was
// created, changed (vs the prior snapshot), or untouched. The "prev"
// snapshot is returned so callers can render a diff in the change
// notification.
func (r *MongoCalendarEventRepo) UpsertState(ctx context.Context, s *CalendarEventState) (CalendarEventStateChange, error) {
	if s == nil {
		return CalendarEventStateChange{}, errors.New("nil state")
	}
	filter := bson.M{
		"telegram_user_id": s.TelegramUserID,
		"event_uid":        s.UID,
		"instance_ts":      s.InstanceTime,
	}
	now := time.Now().Unix()
	s.SeenAt = now

	prev, err := r.findOne(ctx, filter)
	if err != nil {
		return CalendarEventStateChange{}, err
	}

	change := CalendarEventStateChange{Prev: prev}
	if prev == nil {
		change.Created = true
	} else {
		change.Changed = prev.Summary != s.Summary ||
			prev.Start != s.Start ||
			prev.End != s.End ||
			prev.Location != s.Location ||
			prev.Sequence < s.Sequence ||
			(s.LastModified > 0 && prev.LastModified < s.LastModified)
	}

	setDoc := bson.M{
		"summary":       s.Summary,
		"start_ts":      s.Start,
		"end_ts":        s.End,
		"location":      s.Location,
		"sequence":      s.Sequence,
		"last_modified": s.LastModified,
		"seen_at":       now,
	}
	if s.ReminderSent {
		setDoc["reminder_sent"] = true
	}
	if s.NotifiedNew {
		setDoc["notified_new"] = true
	}

	update := bson.M{
		"$set": setDoc,
		"$setOnInsert": bson.M{
			"telegram_user_id": s.TelegramUserID,
			"event_uid":        s.UID,
			"instance_ts":      s.InstanceTime,
		},
	}
	opts := options.UpdateOne().SetUpsert(true)
	if _, err := r.coll.UpdateOne(ctx, filter, update, opts); err != nil {
		return CalendarEventStateChange{}, err
	}
	return change, nil
}

func (r *MongoCalendarEventRepo) findOne(ctx context.Context, filter bson.M) (*CalendarEventState, error) {
	var doc CalendarEventState
	err := r.coll.FindOne(ctx, filter).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &doc, nil
}

// GetByUserAndInstance returns nil when the row doesn't exist (no
// error). Used by callers that want to inspect the durable
// ReminderSent/NotifiedNew flags before sending a notification.
func (r *MongoCalendarEventRepo) GetByUserAndInstance(ctx context.Context, uid int64, eventUID string, instanceTS int64) (*CalendarEventState, error) {
	return r.findOne(ctx, bson.M{
		"telegram_user_id": uid,
		"event_uid":        eventUID,
		"instance_ts":      instanceTS,
	})
}

// MarkReminderSent flips reminder_sent=true atomically so a follow-up
// poll on a flapping schedule cannot re-fire a reminder. Idempotent.
func (r *MongoCalendarEventRepo) MarkReminderSent(ctx context.Context, uid int64, eventUID string, instanceTS int64) error {
	_, err := r.coll.UpdateOne(ctx, bson.M{
		"telegram_user_id": uid,
		"event_uid":        eventUID,
		"instance_ts":      instanceTS,
	}, bson.M{
		"$set": bson.M{"reminder_sent": true, "seen_at": time.Now().Unix()},
	})
	return err
}

// MarkNotifiedNew is the durable counterpart of "new event"
// notifications — used both to remember that a real notification was
// emitted and to suppress the first-poll backfill from spamming the
// user about every existing event.
func (r *MongoCalendarEventRepo) MarkNotifiedNew(ctx context.Context, uid int64, eventUID string, instanceTS int64) error {
	_, err := r.coll.UpdateOne(ctx, bson.M{
		"telegram_user_id": uid,
		"event_uid":        eventUID,
		"instance_ts":      instanceTS,
	}, bson.M{
		"$set": bson.M{"notified_new": true, "seen_at": time.Now().Unix()},
	})
	return err
}

// DeleteStaleByUser removes rows whose Start is strictly before
// keepStartTSAfter — old, expired instances we'll never need to
// diff against again. Bounded scan via the user_start_ts index.
func (r *MongoCalendarEventRepo) DeleteStaleByUser(ctx context.Context, uid int64, keepStartTSAfter int64) error {
	_, err := r.coll.DeleteMany(ctx, bson.M{
		"telegram_user_id": uid,
		"start_ts":         bson.M{"$lt": keepStartTSAfter},
	})
	return err
}

// DeleteAllByUser wipes every row for the user. Called from the
// "Remove calendar" UX path so a re-set starts from a clean slate.
func (r *MongoCalendarEventRepo) DeleteAllByUser(ctx context.Context, uid int64) error {
	_, err := r.coll.DeleteMany(ctx, bson.M{"telegram_user_id": uid})
	return err
}
