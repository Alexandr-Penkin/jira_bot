package preferences_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"SleepJiraBot/internal/preferences"
	"SleepJiraBot/internal/storage"
	preferencesv1 "SleepJiraBot/pkg/preferencesv1"
)

type fakeUserStore struct {
	mu   sync.Mutex
	user *storage.User
	err  error
	sets []string
}

func (f *fakeUserStore) record(op string) {
	f.sets = append(f.sets, op)
}

func (f *fakeUserStore) GetByTelegramID(_ context.Context, id int64) (*storage.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	if f.user == nil || f.user.TelegramUserID != id {
		return nil, nil
	}
	c := *f.user
	return &c, nil
}

func (f *fakeUserStore) SetLanguage(_ context.Context, id int64, lang string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureUser(id)
	f.user.Language = lang
	f.record("lang")
	return nil
}

func (f *fakeUserStore) SetDefaults(_ context.Context, id int64, project string, boardID int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureUser(id)
	f.user.DefaultProject = project
	f.user.DefaultBoardID = boardID
	f.record("defaults")
	return nil
}

func (f *fakeUserStore) SetSprintIssueTypes(_ context.Context, id int64, types []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureUser(id)
	f.user.SprintIssueTypes = types
	f.record("sprint")
	return nil
}

func (f *fakeUserStore) SetDoneStatuses(_ context.Context, id int64, statuses []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureUser(id)
	f.user.DoneStatuses = statuses
	f.record("done")
	return nil
}

func (f *fakeUserStore) SetHoldStatuses(_ context.Context, id int64, statuses []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureUser(id)
	f.user.HoldStatuses = statuses
	f.record("hold")
	return nil
}

func (f *fakeUserStore) SetAssigneeField(_ context.Context, id int64, field string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureUser(id)
	f.user.AssigneeFieldID = field
	f.record("assignee")
	return nil
}

func (f *fakeUserStore) SetStoryPointsField(_ context.Context, id int64, field string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureUser(id)
	f.user.StoryPointsFieldID = field
	f.record("story")
	return nil
}

func (f *fakeUserStore) SetDailyJQL(_ context.Context, id int64, done, doing, plan string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureUser(id)
	f.user.DailyDoneJQL = done
	f.user.DailyDoingJQL = doing
	f.user.DailyPlanJQL = plan
	f.record("daily")
	return nil
}

func (f *fakeUserStore) ensureUser(id int64) {
	if f.user == nil {
		f.user = &storage.User{TelegramUserID: id}
	}
}

func newProvider(t *testing.T) (*preferences.LocalProvider, *fakeUserStore) {
	t.Helper()
	store := &fakeUserStore{}
	return preferences.NewLocalProvider(store, zerolog.Nop()), store
}

func TestLocalProvider_Get_NotFound(t *testing.T) {
	p, _ := newProvider(t)
	_, err := p.Get(context.Background(), 42)
	require.Error(t, err)
	var perr *preferences.ProviderError
	require.ErrorAs(t, err, &perr)
	assert.Equal(t, preferencesv1.ErrCodeNotFound, perr.Code)
}

func TestLocalProvider_Get_InvalidID(t *testing.T) {
	p, _ := newProvider(t)
	_, err := p.Get(context.Background(), 0)
	var perr *preferences.ProviderError
	require.ErrorAs(t, err, &perr)
	assert.Equal(t, preferencesv1.ErrCodeInvalidRequest, perr.Code)
}

func TestLocalProvider_Get_ReturnsSnapshot(t *testing.T) {
	p, store := newProvider(t)
	store.user = &storage.User{
		TelegramUserID:   42,
		Language:         "en",
		DefaultProject:   "PROJ",
		DefaultBoardID:   7,
		SprintIssueTypes: []string{"Task", "Bug"},
		DoneStatuses:     []string{"Done"},
	}
	got, err := p.Get(context.Background(), 42)
	require.NoError(t, err)
	assert.Equal(t, "en", got.Language)
	assert.Equal(t, "PROJ", got.DefaultProject)
	assert.Equal(t, 7, got.DefaultBoardID)
	assert.Equal(t, []string{"Task", "Bug"}, got.SprintIssueTypes)
	assert.Equal(t, []string{"Done"}, got.DoneStatuses)
}

func TestLocalProvider_SetLanguage_DelegatesToStore(t *testing.T) {
	p, store := newProvider(t)
	require.NoError(t, p.SetLanguage(context.Background(), 42, "ru"))
	assert.Equal(t, "ru", store.user.Language)
	assert.Equal(t, []string{"lang"}, store.sets)
}

func TestLocalProvider_SetDefaults_DelegatesToStore(t *testing.T) {
	p, store := newProvider(t)
	require.NoError(t, p.SetDefaults(context.Background(), 42, "PROJ", 3))
	assert.Equal(t, "PROJ", store.user.DefaultProject)
	assert.Equal(t, 3, store.user.DefaultBoardID)
	assert.Equal(t, []string{"defaults"}, store.sets)
}

func TestLocalProvider_AllSettersDelegate(t *testing.T) {
	p, store := newProvider(t)
	ctx := context.Background()
	require.NoError(t, p.SetSprintIssueTypes(ctx, 1, []string{"Task"}))
	require.NoError(t, p.SetDoneStatuses(ctx, 1, []string{"Done"}))
	require.NoError(t, p.SetHoldStatuses(ctx, 1, []string{"Blocked"}))
	require.NoError(t, p.SetAssigneeField(ctx, 1, "customfield_1"))
	require.NoError(t, p.SetStoryPointsField(ctx, 1, "customfield_2"))
	require.NoError(t, p.SetDailyJQL(ctx, 1, "a", "b", "c"))
	assert.Equal(t, []string{"sprint", "done", "hold", "assignee", "story", "daily"}, store.sets)
}

func TestLocalProvider_SetterRejectsZeroID(t *testing.T) {
	p, _ := newProvider(t)
	err := p.SetLanguage(context.Background(), 0, "ru")
	var perr *preferences.ProviderError
	require.ErrorAs(t, err, &perr)
	assert.Equal(t, preferencesv1.ErrCodeInvalidRequest, perr.Code)
}

func TestLocalProvider_GetPropagatesStoreError(t *testing.T) {
	p, store := newProvider(t)
	store.user = &storage.User{TelegramUserID: 42}
	store.err = errors.New("mongo down")
	_, err := p.Get(context.Background(), 42)
	require.Error(t, err)
}
