package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"SleepJiraBot/internal/crypto"
	eventsv1 "SleepJiraBot/pkg/events/v1"
)

type User struct {
	ID              bson.ObjectID `bson:"_id,omitempty"`
	TelegramUserID  int64         `bson:"telegram_user_id"`
	JiraCloudID     string        `bson:"jira_cloud_id,omitempty"`
	JiraAccountID   string        `bson:"jira_account_id,omitempty"`
	JiraDisplayName string        `bson:"jira_display_name,omitempty"`
	JiraSiteURL     string        `bson:"jira_site_url,omitempty"`
	AccessToken     string        `bson:"access_token,omitempty"`
	RefreshToken    string        `bson:"refresh_token,omitempty"`
	TokenExpiresAt  time.Time     `bson:"token_expires_at,omitempty"`
	// GrantedScopes is the space-separated scope string Atlassian returned
	// at token issuance. Captured so /diagnose can show the actual grant vs.
	// the currently-configured required set without having to re-trigger
	// OAuth. Atlassian binds scopes into the access token at issuance and
	// refresh preserves them — so a stale value here is itself diagnostic.
	GrantedScopes        string   `bson:"granted_scopes,omitempty"`
	Language             string   `bson:"language,omitempty"`
	DefaultProject       string   `bson:"default_project,omitempty"`
	DefaultBoardID       int      `bson:"default_board_id,omitempty"`
	DefaultIssueTypeID   string   `bson:"default_issue_type_id,omitempty"`
	DefaultIssueTypeName string   `bson:"default_issue_type_name,omitempty"`
	SprintIssueTypes     []string `bson:"sprint_issue_types,omitempty"`
	AssigneeFieldID      string   `bson:"assignee_field_id,omitempty"`
	StoryPointsFieldID   string   `bson:"story_points_field_id,omitempty"`
	DailyDoneJQL         string   `bson:"daily_done_jql,omitempty"`
	DailyDoingJQL        string   `bson:"daily_doing_jql,omitempty"`
	DailyPlanJQL         string   `bson:"daily_plan_jql,omitempty"`
	DoneStatuses         []string `bson:"done_statuses,omitempty"`
	HoldStatuses         []string `bson:"hold_statuses,omitempty"`
	// CalendarURL stores the encrypted ICS feed URL the user wants the
	// bot to poll. Sensitive: anyone holding the URL can read the
	// calendar, so we keep it under the same AES-GCM envelope as the
	// Jira OAuth tokens. Empty means the feature is off for the user.
	CalendarURL             string `bson:"calendar_url,omitempty"`
	CalendarNotifyEnabled   bool   `bson:"calendar_notify_enabled,omitempty"`
	CalendarReminderMinutes int    `bson:"calendar_reminder_minutes,omitempty"`
	CalendarLastFetchedAt   int64  `bson:"calendar_last_fetched_at,omitempty"`
	CalendarLastError       string `bson:"calendar_last_error,omitempty"`
	CreatedTS               int64  `bson:"created_ts"`
	ModifiedTS              int64  `bson:"modified_ts"`
}

const (
	// DefaultCalendarReminderMinutes is what we seed CalendarReminderMinutes
	// with on first set. 15 min is the same default Google Calendar uses
	// for new event notifications.
	DefaultCalendarReminderMinutes = 15
	// maxCalendarLastErrorLen caps the truncated error string written
	// back to the user document — the field is shown in the profile
	// UI, so we keep it small.
	maxCalendarLastErrorLen = 200
)

type UserRepo struct {
	coll *mongo.Collection
	enc  *crypto.Encryptor
	pub  eventsv1.Publisher
}

func NewUserRepo(db *mongo.Database, enc *crypto.Encryptor) *UserRepo {
	return &UserRepo{coll: db.Collection("users"), enc: enc, pub: eventsv1.NoopPublisher{}}
}

// SetEventPublisher installs a domain event publisher. UserDeauthorized
// fires after a successful ClearJiraCredentials so downstream services
// (subscription-svc, scheduler-svc) can prune resources that no longer
// have a token to act on.
func (r *UserRepo) SetEventPublisher(p eventsv1.Publisher) {
	if p == nil {
		r.pub = eventsv1.NoopPublisher{}
		return
	}
	r.pub = p
}

func (r *UserRepo) GetByTelegramID(ctx context.Context, telegramUserID int64) (*User, error) {
	var user User
	err := r.coll.FindOne(ctx, bson.M{"telegram_user_id": telegramUserID}).Decode(&user)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if err = r.decryptTokens(&user); err != nil {
		return nil, fmt.Errorf("decrypt tokens: %w", err)
	}
	if err = r.decryptCalendar(&user); err != nil {
		// A corrupted CalendarURL must not bounce a user out of Jira
		// auth — log via the returned error and continue with the
		// field cleared so the rest of the profile still works.
		user.CalendarURL = ""
		user.CalendarLastError = "decrypt calendar url: " + err.Error()
	}

	return &user, nil
}

func (r *UserRepo) Upsert(ctx context.Context, user *User) error {
	now := time.Now().Unix()

	encAccess, err := r.enc.Encrypt(user.AccessToken)
	if err != nil {
		return fmt.Errorf("encrypt access token: %w", err)
	}
	encRefresh, err := r.enc.Encrypt(user.RefreshToken)
	if err != nil {
		return fmt.Errorf("encrypt refresh token: %w", err)
	}

	filter := bson.M{"telegram_user_id": user.TelegramUserID}
	update := bson.M{
		"$set": bson.M{
			"jira_cloud_id":     user.JiraCloudID,
			"jira_account_id":   user.JiraAccountID,
			"jira_display_name": user.JiraDisplayName,
			"jira_site_url":     user.JiraSiteURL,
			"access_token":      encAccess,
			"refresh_token":     encRefresh,
			"token_expires_at":  user.TokenExpiresAt,
			"granted_scopes":    user.GrantedScopes,
			"modified_ts":       now,
		},
		"$setOnInsert": bson.M{
			"created_ts": now,
		},
	}

	opts := options.UpdateOne().SetUpsert(true)
	_, err = r.coll.UpdateOne(ctx, filter, update, opts)
	return err
}

// UpdateGrantedScopes records the scope string Atlassian returned with the
// latest token. Called separately from UpdateTokens because the refresh
// response sometimes omits the scope field — only overwrite when we have a
// non-empty value to avoid stomping the connect-time grant with "".
func (r *UserRepo) UpdateGrantedScopes(ctx context.Context, telegramUserID int64, scopes string) error {
	if scopes == "" {
		return nil
	}
	filter := bson.M{"telegram_user_id": telegramUserID}
	update := bson.M{
		"$set": bson.M{
			"granted_scopes": scopes,
			"modified_ts":    time.Now().Unix(),
		},
	}
	_, err := r.coll.UpdateOne(ctx, filter, update)
	return err
}

func (r *UserRepo) UpdateTokens(ctx context.Context, telegramUserID int64, accessToken, refreshToken string, expiresAt time.Time) error {
	encAccess, err := r.enc.Encrypt(accessToken)
	if err != nil {
		return fmt.Errorf("encrypt access token: %w", err)
	}
	encRefresh, err := r.enc.Encrypt(refreshToken)
	if err != nil {
		return fmt.Errorf("encrypt refresh token: %w", err)
	}

	filter := bson.M{"telegram_user_id": telegramUserID}
	update := bson.M{
		"$set": bson.M{
			"access_token":     encAccess,
			"refresh_token":    encRefresh,
			"token_expires_at": expiresAt,
			"modified_ts":      time.Now().Unix(),
		},
	}
	_, err = r.coll.UpdateOne(ctx, filter, update)
	return err
}

// SetJiraIdentity records the Jira account id and display name for a user.
// Used both during OAuth connect and as a backfill path when older user
// records do not yet have a display name stored.
func (r *UserRepo) SetJiraIdentity(ctx context.Context, telegramUserID int64, accountID, displayName string) error {
	filter := bson.M{"telegram_user_id": telegramUserID}
	update := bson.M{
		"$set": bson.M{
			"jira_account_id":   accountID,
			"jira_display_name": displayName,
			"modified_ts":       time.Now().Unix(),
		},
	}
	_, err := r.coll.UpdateOne(ctx, filter, update)
	return err
}

func (r *UserRepo) SetLanguage(ctx context.Context, telegramUserID int64, lang string) error {
	now := time.Now().Unix()
	filter := bson.M{"telegram_user_id": telegramUserID}
	update := bson.M{
		"$set": bson.M{
			"language":    lang,
			"modified_ts": now,
		},
		"$setOnInsert": bson.M{
			"created_ts": now,
		},
	}
	opts := options.UpdateOne().SetUpsert(true)
	if _, err := r.coll.UpdateOne(ctx, filter, update, opts); err != nil {
		return err
	}
	_ = r.pub.Publish(ctx, eventsv1.LanguageChanged{
		TelegramID: telegramUserID,
		Language:   lang,
		At:         now,
	}, "")
	return nil
}

func (r *UserRepo) SetDefaults(ctx context.Context, telegramUserID int64, project string, boardID int) error {
	filter := bson.M{"telegram_user_id": telegramUserID}
	update := bson.M{
		"$set": bson.M{
			"default_project":  project,
			"default_board_id": boardID,
			"modified_ts":      time.Now().Unix(),
		},
	}
	if _, err := r.coll.UpdateOne(ctx, filter, update); err != nil {
		return err
	}
	r.publishDefaultsChanged(ctx, telegramUserID)
	return nil
}

// SetDefaultIssueType stores the issue type used by /createfast when the
// user doesn't spell one out. Empty typeID clears the preference.
func (r *UserRepo) SetDefaultIssueType(ctx context.Context, telegramUserID int64, typeID, typeName string) error {
	filter := bson.M{"telegram_user_id": telegramUserID}
	update := bson.M{
		"$set": bson.M{
			"default_issue_type_id":   typeID,
			"default_issue_type_name": typeName,
			"modified_ts":             time.Now().Unix(),
		},
	}
	if _, err := r.coll.UpdateOne(ctx, filter, update); err != nil {
		return err
	}
	r.publishDefaultsChanged(ctx, telegramUserID)
	return nil
}

func (r *UserRepo) SetSprintIssueTypes(ctx context.Context, telegramUserID int64, issueTypes []string) error {
	filter := bson.M{"telegram_user_id": telegramUserID}
	update := bson.M{
		"$set": bson.M{
			"sprint_issue_types": issueTypes,
			"modified_ts":        time.Now().Unix(),
		},
	}
	if _, err := r.coll.UpdateOne(ctx, filter, update); err != nil {
		return err
	}
	r.publishDefaultsChanged(ctx, telegramUserID)
	return nil
}

func (r *UserRepo) SetDoneStatuses(ctx context.Context, telegramUserID int64, statuses []string) error {
	filter := bson.M{"telegram_user_id": telegramUserID}
	update := bson.M{
		"$set": bson.M{
			"done_statuses": statuses,
			"modified_ts":   time.Now().Unix(),
		},
	}
	if _, err := r.coll.UpdateOne(ctx, filter, update); err != nil {
		return err
	}
	r.publishDefaultsChanged(ctx, telegramUserID)
	return nil
}

func (r *UserRepo) SetHoldStatuses(ctx context.Context, telegramUserID int64, statuses []string) error {
	filter := bson.M{"telegram_user_id": telegramUserID}
	update := bson.M{
		"$set": bson.M{
			"hold_statuses": statuses,
			"modified_ts":   time.Now().Unix(),
		},
	}
	if _, err := r.coll.UpdateOne(ctx, filter, update); err != nil {
		return err
	}
	r.publishDefaultsChanged(ctx, telegramUserID)
	return nil
}

func (r *UserRepo) SetAssigneeField(ctx context.Context, telegramUserID int64, fieldID string) error {
	filter := bson.M{"telegram_user_id": telegramUserID}
	update := bson.M{
		"$set": bson.M{
			"assignee_field_id": fieldID,
			"modified_ts":       time.Now().Unix(),
		},
	}
	if _, err := r.coll.UpdateOne(ctx, filter, update); err != nil {
		return err
	}
	r.publishDefaultsChanged(ctx, telegramUserID)
	return nil
}

func (r *UserRepo) SetStoryPointsField(ctx context.Context, telegramUserID int64, fieldID string) error {
	filter := bson.M{"telegram_user_id": telegramUserID}
	update := bson.M{
		"$set": bson.M{
			"story_points_field_id": fieldID,
			"modified_ts":           time.Now().Unix(),
		},
	}
	if _, err := r.coll.UpdateOne(ctx, filter, update); err != nil {
		return err
	}
	r.publishDefaultsChanged(ctx, telegramUserID)
	return nil
}

// GetByJiraAccountIDs returns users matching the given Jira account IDs.
func (r *UserRepo) GetByJiraAccountIDs(ctx context.Context, accountIDs []string) ([]User, error) {
	if len(accountIDs) == 0 {
		return nil, nil
	}
	cursor, err := r.coll.Find(ctx, bson.M{
		"jira_account_id": bson.M{"$in": accountIDs},
	})
	if err != nil {
		return nil, err
	}

	var users []User
	if err := cursor.All(ctx, &users); err != nil {
		return nil, err
	}

	// Decrypt tokens for each user.
	for i := range users {
		if decErr := r.decryptTokens(&users[i]); decErr != nil {
			return nil, fmt.Errorf("decrypt tokens for user %d: %w", users[i].TelegramUserID, decErr)
		}
		if decErr := r.decryptCalendar(&users[i]); decErr != nil {
			users[i].CalendarURL = ""
			users[i].CalendarLastError = "decrypt calendar url: " + decErr.Error()
		}
	}

	return users, nil
}

func (r *UserRepo) SetDailyJQL(ctx context.Context, telegramUserID int64, doneJQL, doingJQL, planJQL string) error {
	filter := bson.M{"telegram_user_id": telegramUserID}
	update := bson.M{
		"$set": bson.M{
			"daily_done_jql":  doneJQL,
			"daily_doing_jql": doingJQL,
			"daily_plan_jql":  planJQL,
			"modified_ts":     time.Now().Unix(),
		},
	}
	if _, err := r.coll.UpdateOne(ctx, filter, update); err != nil {
		return err
	}
	r.publishDefaultsChanged(ctx, telegramUserID)
	return nil
}

// publishDefaultsChanged reads back the user and publishes a full
// snapshot DefaultsChanged event. Read-after-write is acceptable —
// preference setters are not on the message hot path. If the read fails
// the event is dropped (better than publishing a partial snapshot).
func (r *UserRepo) publishDefaultsChanged(ctx context.Context, telegramUserID int64) {
	var u User
	if err := r.coll.FindOne(ctx, bson.M{"telegram_user_id": telegramUserID}).Decode(&u); err != nil {
		return
	}
	_ = r.pub.Publish(ctx, &eventsv1.DefaultsChanged{
		TelegramID:           telegramUserID,
		DefaultProject:       u.DefaultProject,
		DefaultBoardID:       u.DefaultBoardID,
		DefaultIssueTypeID:   u.DefaultIssueTypeID,
		DefaultIssueTypeName: u.DefaultIssueTypeName,
		SprintIssueTypes:     u.SprintIssueTypes,
		AssigneeFieldID:      u.AssigneeFieldID,
		StoryPointsFieldID:   u.StoryPointsFieldID,
		DoneStatuses:         u.DoneStatuses,
		HoldStatuses:         u.HoldStatuses,
		DailyDoneJQL:         u.DailyDoneJQL,
		DailyDoingJQL:        u.DailyDoingJQL,
		DailyPlanJQL:         u.DailyPlanJQL,
		At:                   time.Now().Unix(),
	}, "")
}

func (r *UserRepo) DeleteByTelegramID(ctx context.Context, telegramUserID int64) error {
	_, err := r.coll.DeleteOne(ctx, bson.M{"telegram_user_id": telegramUserID})
	return err
}

// ClearJiraCredentials removes the Jira-side identity (cloud id, account
// id, site URL, OAuth tokens) but keeps the user's preferences — language,
// default project/board, sprint issue types, assignee/story points field
// ids, daily JQLs. This is the method used on user-initiated /disconnect
// so that a subsequent /connect does not force the user to rebuild their
// setup from scratch.
func (r *UserRepo) ClearJiraCredentials(ctx context.Context, telegramUserID int64) error {
	filter := bson.M{"telegram_user_id": telegramUserID}
	update := bson.M{
		"$set": bson.M{
			"modified_ts": time.Now().Unix(),
		},
		"$unset": bson.M{
			"jira_cloud_id":    "",
			"jira_account_id":  "",
			"jira_site_url":    "",
			"access_token":     "",
			"refresh_token":    "",
			"token_expires_at": "",
		},
	}
	if _, err := r.coll.UpdateOne(ctx, filter, update); err != nil {
		return err
	}

	_ = r.pub.Publish(ctx, eventsv1.UserDeauthorized{
		TelegramID: telegramUserID,
		At:         time.Now().Unix(),
	}, "")
	return nil
}

// CountAll returns the total number of users.
func (r *UserRepo) CountAll(ctx context.Context) (int64, error) {
	return r.coll.CountDocuments(ctx, bson.M{})
}

// CountConnected returns the number of users with a non-empty access token.
func (r *UserRepo) CountConnected(ctx context.Context) (int64, error) {
	return r.coll.CountDocuments(ctx, bson.M{
		"access_token": bson.M{"$ne": ""},
	})
}

// ListAll returns all users without decrypting tokens.
func (r *UserRepo) ListAll(ctx context.Context, skip, limit int64) ([]User, error) {
	opts := options.Find().
		SetSort(bson.M{"created_ts": -1}).
		SetSkip(skip).
		SetLimit(limit)
	cursor, err := r.coll.Find(ctx, bson.M{}, opts)
	if err != nil {
		return nil, err
	}
	var users []User
	if err := cursor.All(ctx, &users); err != nil {
		return nil, err
	}
	return users, nil
}

func (r *UserRepo) decryptTokens(user *User) error {
	var err error
	user.AccessToken, err = r.enc.Decrypt(user.AccessToken)
	if err != nil {
		return fmt.Errorf("access token: %w", err)
	}
	user.RefreshToken, err = r.enc.Decrypt(user.RefreshToken)
	if err != nil {
		return fmt.Errorf("refresh token: %w", err)
	}
	return nil
}

func (r *UserRepo) decryptCalendar(user *User) error {
	if user.CalendarURL == "" {
		return nil
	}
	plain, err := r.enc.Decrypt(user.CalendarURL)
	if err != nil {
		return err
	}
	user.CalendarURL = plain
	return nil
}

// SetCalendarURL stores an encrypted ICS feed URL. Passing an empty
// rawURL clears the calendar configuration (URL, enabled flag, last
// fetched-at/error). On first set we default the enabled flag to true
// and the reminder window to DefaultCalendarReminderMinutes so the user
// gets notifications without an extra menu trip.
func (r *UserRepo) SetCalendarURL(ctx context.Context, telegramUserID int64, rawURL string) error {
	now := time.Now().Unix()
	filter := bson.M{"telegram_user_id": telegramUserID}

	if rawURL == "" {
		update := bson.M{
			"$set": bson.M{
				"modified_ts": now,
			},
			"$unset": bson.M{
				"calendar_url":              "",
				"calendar_notify_enabled":   "",
				"calendar_reminder_minutes": "",
				"calendar_last_fetched_at":  "",
				"calendar_last_error":       "",
			},
		}
		_, err := r.coll.UpdateOne(ctx, filter, update)
		return err
	}

	encURL, err := r.enc.Encrypt(rawURL)
	if err != nil {
		return fmt.Errorf("encrypt calendar url: %w", err)
	}

	// Read current state so we only seed defaults on the first set,
	// otherwise an SetCalendarURL after a toggle-off would silently
	// turn notifications back on.
	var existing User
	getErr := r.coll.FindOne(ctx, filter).Decode(&existing)

	setDoc := bson.M{
		"calendar_url":             encURL,
		"calendar_last_fetched_at": int64(0),
		"calendar_last_error":      "",
		"modified_ts":              now,
	}
	if errors.Is(getErr, mongo.ErrNoDocuments) || existing.CalendarReminderMinutes == 0 {
		setDoc["calendar_notify_enabled"] = true
		setDoc["calendar_reminder_minutes"] = DefaultCalendarReminderMinutes
	}

	update := bson.M{
		"$set": setDoc,
		"$setOnInsert": bson.M{
			"created_ts": now,
		},
	}
	opts := options.UpdateOne().SetUpsert(true)
	_, err = r.coll.UpdateOne(ctx, filter, update, opts)
	return err
}

// SetCalendarNotifyEnabled toggles the notification gate without
// changing the stored URL. Off keeps the URL on disk so the user can
// flip it back on without re-entering it.
func (r *UserRepo) SetCalendarNotifyEnabled(ctx context.Context, telegramUserID int64, enabled bool) error {
	filter := bson.M{"telegram_user_id": telegramUserID}
	update := bson.M{
		"$set": bson.M{
			"calendar_notify_enabled": enabled,
			"modified_ts":             time.Now().Unix(),
		},
	}
	_, err := r.coll.UpdateOne(ctx, filter, update)
	return err
}

// SetCalendarReminderMinutes records the lead time the user wants
// before each event. Clamped to a sane [1, 1440] range so a bad input
// cannot turn the poller into a degenerate burst-sender.
func (r *UserRepo) SetCalendarReminderMinutes(ctx context.Context, telegramUserID int64, mins int) error {
	if mins < 1 {
		mins = 1
	}
	if mins > 1440 {
		mins = 1440
	}
	filter := bson.M{"telegram_user_id": telegramUserID}
	update := bson.M{
		"$set": bson.M{
			"calendar_reminder_minutes": mins,
			"modified_ts":               time.Now().Unix(),
		},
	}
	_, err := r.coll.UpdateOne(ctx, filter, update)
	return err
}

// MarkCalendarFetched records the timestamp of the last poll attempt
// and the truncated error string (empty on success). Used by the
// calendar poller as both a heartbeat and a way to surface fetch
// failures in the profile UI.
func (r *UserRepo) MarkCalendarFetched(ctx context.Context, telegramUserID int64, errMsg string) error {
	if len(errMsg) > maxCalendarLastErrorLen {
		errMsg = errMsg[:maxCalendarLastErrorLen]
	}
	filter := bson.M{"telegram_user_id": telegramUserID}
	update := bson.M{
		"$set": bson.M{
			"calendar_last_fetched_at": time.Now().Unix(),
			"calendar_last_error":      errMsg,
			"modified_ts":              time.Now().Unix(),
		},
	}
	_, err := r.coll.UpdateOne(ctx, filter, update)
	return err
}

// ListUsersWithCalendar returns every user with a non-empty
// CalendarURL AND notifications enabled. The poller consumes this list
// every tick — calendars are sparse so a per-user filter avoids
// scanning the entire users collection.
func (r *UserRepo) ListUsersWithCalendar(ctx context.Context) ([]User, error) {
	cursor, err := r.coll.Find(ctx, bson.M{
		"calendar_url":            bson.M{"$exists": true, "$ne": ""},
		"calendar_notify_enabled": true,
	})
	if err != nil {
		return nil, err
	}

	var users []User
	if err := cursor.All(ctx, &users); err != nil {
		return nil, err
	}
	for i := range users {
		if decErr := r.decryptCalendar(&users[i]); decErr != nil {
			users[i].CalendarURL = ""
			users[i].CalendarLastError = "decrypt calendar url: " + decErr.Error()
		}
	}
	return users, nil
}
