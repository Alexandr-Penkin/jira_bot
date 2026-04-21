// Package preferences owns the Phase-5 user-preferences aggregate.
// LocalProvider wraps UserRepo so both the monolith and cmd/preferences-svc
// share one implementation. A future Phase 5b extraction moves the
// preference fields into a dedicated user_preferences collection; until
// then both processes share the users collection in Mongo.
//
// Event emission (LanguageChanged, DefaultsChanged) lives in UserRepo
// setters so direct UserRepo callers in the monolith also fire events —
// LocalProvider is intentionally event-agnostic to avoid double-publishing.
package preferences

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"

	"SleepJiraBot/internal/storage"
	preferencesv1 "SleepJiraBot/pkg/preferencesv1"
)

// Provider is the minimal surface callers need. LocalProvider is the
// only impl for now; a remote HTTP client will live in pkg/preferencesclient.
// The method set mirrors UserStore on purpose — Provider is the
// caller-facing seam, UserStore is the storage-facing seam, and they stay
// in lock-step so the LocalProvider can forward calls 1-to-1 without a
// translation layer.
//
//nolint:dupl // structural twin of UserStore below; see doc-comment above.
type Provider interface {
	Get(ctx context.Context, telegramID int64) (*preferencesv1.Preferences, error)
	SetLanguage(ctx context.Context, telegramID int64, lang string) error
	SetDefaults(ctx context.Context, telegramID int64, project string, boardID int) error
	SetDefaultIssueType(ctx context.Context, telegramID int64, typeID, typeName string) error
	SetSprintIssueTypes(ctx context.Context, telegramID int64, issueTypes []string) error
	SetDoneStatuses(ctx context.Context, telegramID int64, statuses []string) error
	SetHoldStatuses(ctx context.Context, telegramID int64, statuses []string) error
	SetAssigneeField(ctx context.Context, telegramID int64, fieldID string) error
	SetStoryPointsField(ctx context.Context, telegramID int64, fieldID string) error
	SetDailyJQL(ctx context.Context, telegramID int64, doneJQL, doingJQL, planJQL string) error
}

// UserStore is the UserRepo surface LocalProvider touches. Kept as an
// interface so tests can inject a fake without Mongo.
//
//nolint:dupl // structural twin of Provider above; intentional, see doc.
type UserStore interface {
	GetByTelegramID(ctx context.Context, telegramUserID int64) (*storage.User, error)
	SetLanguage(ctx context.Context, telegramUserID int64, lang string) error
	SetDefaults(ctx context.Context, telegramUserID int64, project string, boardID int) error
	SetDefaultIssueType(ctx context.Context, telegramUserID int64, typeID, typeName string) error
	SetSprintIssueTypes(ctx context.Context, telegramUserID int64, issueTypes []string) error
	SetDoneStatuses(ctx context.Context, telegramUserID int64, statuses []string) error
	SetHoldStatuses(ctx context.Context, telegramUserID int64, statuses []string) error
	SetAssigneeField(ctx context.Context, telegramUserID int64, fieldID string) error
	SetStoryPointsField(ctx context.Context, telegramUserID int64, fieldID string) error
	SetDailyJQL(ctx context.Context, telegramUserID int64, doneJQL, doingJQL, planJQL string) error
}

type LocalProvider struct {
	userRepo UserStore
	log      zerolog.Logger
}

func NewLocalProvider(userRepo UserStore, log zerolog.Logger) *LocalProvider {
	return &LocalProvider{
		userRepo: userRepo,
		log:      log,
	}
}

func (p *LocalProvider) Get(ctx context.Context, telegramID int64) (*preferencesv1.Preferences, error) {
	if telegramID == 0 {
		return nil, &ProviderError{Code: preferencesv1.ErrCodeInvalidRequest, Message: "telegram_id is required"}
	}
	user, err := p.userRepo.GetByTelegramID(ctx, telegramID)
	if err != nil {
		return nil, fmt.Errorf("read user: %w", err)
	}
	if user == nil {
		return nil, &ProviderError{Code: preferencesv1.ErrCodeNotFound, Message: "user not found"}
	}
	return userToPreferences(user), nil
}

func (p *LocalProvider) SetLanguage(ctx context.Context, telegramID int64, lang string) error {
	if telegramID == 0 {
		return &ProviderError{Code: preferencesv1.ErrCodeInvalidRequest, Message: "telegram_id is required"}
	}
	if err := p.userRepo.SetLanguage(ctx, telegramID, lang); err != nil {
		return fmt.Errorf("set language: %w", err)
	}
	return nil
}

func (p *LocalProvider) SetDefaults(ctx context.Context, telegramID int64, project string, boardID int) error {
	if telegramID == 0 {
		return &ProviderError{Code: preferencesv1.ErrCodeInvalidRequest, Message: "telegram_id is required"}
	}
	if err := p.userRepo.SetDefaults(ctx, telegramID, project, boardID); err != nil {
		return fmt.Errorf("set defaults: %w", err)
	}
	return nil
}

func (p *LocalProvider) SetDefaultIssueType(ctx context.Context, telegramID int64, typeID, typeName string) error {
	if telegramID == 0 {
		return &ProviderError{Code: preferencesv1.ErrCodeInvalidRequest, Message: "telegram_id is required"}
	}
	if err := p.userRepo.SetDefaultIssueType(ctx, telegramID, typeID, typeName); err != nil {
		return fmt.Errorf("set default issue type: %w", err)
	}
	return nil
}

func (p *LocalProvider) SetSprintIssueTypes(ctx context.Context, telegramID int64, issueTypes []string) error {
	if telegramID == 0 {
		return &ProviderError{Code: preferencesv1.ErrCodeInvalidRequest, Message: "telegram_id is required"}
	}
	if err := p.userRepo.SetSprintIssueTypes(ctx, telegramID, issueTypes); err != nil {
		return fmt.Errorf("set sprint issue types: %w", err)
	}
	return nil
}

func (p *LocalProvider) SetDoneStatuses(ctx context.Context, telegramID int64, statuses []string) error {
	if telegramID == 0 {
		return &ProviderError{Code: preferencesv1.ErrCodeInvalidRequest, Message: "telegram_id is required"}
	}
	if err := p.userRepo.SetDoneStatuses(ctx, telegramID, statuses); err != nil {
		return fmt.Errorf("set done statuses: %w", err)
	}
	return nil
}

func (p *LocalProvider) SetHoldStatuses(ctx context.Context, telegramID int64, statuses []string) error {
	if telegramID == 0 {
		return &ProviderError{Code: preferencesv1.ErrCodeInvalidRequest, Message: "telegram_id is required"}
	}
	if err := p.userRepo.SetHoldStatuses(ctx, telegramID, statuses); err != nil {
		return fmt.Errorf("set hold statuses: %w", err)
	}
	return nil
}

func (p *LocalProvider) SetAssigneeField(ctx context.Context, telegramID int64, fieldID string) error {
	if telegramID == 0 {
		return &ProviderError{Code: preferencesv1.ErrCodeInvalidRequest, Message: "telegram_id is required"}
	}
	if err := p.userRepo.SetAssigneeField(ctx, telegramID, fieldID); err != nil {
		return fmt.Errorf("set assignee field: %w", err)
	}
	return nil
}

func (p *LocalProvider) SetStoryPointsField(ctx context.Context, telegramID int64, fieldID string) error {
	if telegramID == 0 {
		return &ProviderError{Code: preferencesv1.ErrCodeInvalidRequest, Message: "telegram_id is required"}
	}
	if err := p.userRepo.SetStoryPointsField(ctx, telegramID, fieldID); err != nil {
		return fmt.Errorf("set story points field: %w", err)
	}
	return nil
}

func (p *LocalProvider) SetDailyJQL(ctx context.Context, telegramID int64, doneJQL, doingJQL, planJQL string) error {
	if telegramID == 0 {
		return &ProviderError{Code: preferencesv1.ErrCodeInvalidRequest, Message: "telegram_id is required"}
	}
	if err := p.userRepo.SetDailyJQL(ctx, telegramID, doneJQL, doingJQL, planJQL); err != nil {
		return fmt.Errorf("set daily jql: %w", err)
	}
	return nil
}

func userToPreferences(user *storage.User) *preferencesv1.Preferences {
	return &preferencesv1.Preferences{
		TelegramID:           user.TelegramUserID,
		Language:             user.Language,
		DefaultProject:       user.DefaultProject,
		DefaultBoardID:       user.DefaultBoardID,
		DefaultIssueTypeID:   user.DefaultIssueTypeID,
		DefaultIssueTypeName: user.DefaultIssueTypeName,
		SprintIssueTypes:     user.SprintIssueTypes,
		AssigneeFieldID:      user.AssigneeFieldID,
		StoryPointsFieldID:   user.StoryPointsFieldID,
		DoneStatuses:         user.DoneStatuses,
		HoldStatuses:         user.HoldStatuses,
		DailyDoneJQL:         user.DailyDoneJQL,
		DailyDoingJQL:        user.DailyDoingJQL,
		DailyPlanJQL:         user.DailyPlanJQL,
	}
}

// ProviderError carries a protocol-level error code the Server maps to an
// HTTP status. Non-protocol errors become 500.
type ProviderError struct {
	Code    string
	Message string
}

func (e *ProviderError) Error() string { return e.Code + ": " + e.Message }
