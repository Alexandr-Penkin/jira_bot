package preferences_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"SleepJiraBot/internal/preferences"
	"SleepJiraBot/pkg/preferencesclient"
	preferencesv1 "SleepJiraBot/pkg/preferencesv1"
)

// TestContract_ClientServer_Roundtrip wires the real preferences.Server
// to a real preferencesclient.Client over HTTP (via httptest). If any
// change to paths, request bodies, or response shapes breaks the
// contract, this test catches it — unit tests on each side can't.
func TestContract_ClientServer_Roundtrip(t *testing.T) {
	const token = "shared-secret"

	store := &fakeUserStore{}
	provider := preferences.NewLocalProvider(store, zerolog.Nop())
	srv := preferences.NewServer(provider, token, zerolog.Nop())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	client, err := preferencesclient.New(ts.URL, token, nil)
	require.NoError(t, err)

	ctx := context.Background()
	const tgID = int64(4242)

	// Not-found before any write: maps to preferencesclient.IsNotFound.
	_, err = client.Get(ctx, tgID)
	require.Error(t, err)
	assert.True(t, preferencesclient.IsNotFound(err), "Get on unknown user should surface ErrCodeNotFound")

	require.NoError(t, client.SetLanguage(ctx, tgID, "ru"))
	require.NoError(t, client.SetDefaults(ctx, tgID, "PROJ", 7))
	require.NoError(t, client.SetSprintIssueTypes(ctx, tgID, []string{"Task", "Bug"}))
	require.NoError(t, client.SetDoneStatuses(ctx, tgID, []string{"Done"}))
	require.NoError(t, client.SetHoldStatuses(ctx, tgID, []string{"Blocked"}))
	require.NoError(t, client.SetAssigneeField(ctx, tgID, "customfield_10001"))
	require.NoError(t, client.SetStoryPointsField(ctx, tgID, "customfield_10002"))
	require.NoError(t, client.SetDailyJQL(ctx, tgID, "done jql", "doing jql", "plan jql"))

	got, err := client.Get(ctx, tgID)
	require.NoError(t, err)
	assert.EqualValues(t, tgID, got.TelegramID)
	assert.Equal(t, "ru", got.Language)
	assert.Equal(t, "PROJ", got.DefaultProject)
	assert.Equal(t, 7, got.DefaultBoardID)
	assert.Equal(t, []string{"Task", "Bug"}, got.SprintIssueTypes)
	assert.Equal(t, []string{"Done"}, got.DoneStatuses)
	assert.Equal(t, []string{"Blocked"}, got.HoldStatuses)
	assert.Equal(t, "customfield_10001", got.AssigneeFieldID)
	assert.Equal(t, "customfield_10002", got.StoryPointsFieldID)
	assert.Equal(t, "done jql", got.DailyDoneJQL)
	assert.Equal(t, "doing jql", got.DailyDoingJQL)
	assert.Equal(t, "plan jql", got.DailyPlanJQL)

	assert.Equal(t, []string{"lang", "defaults", "sprint", "done", "hold", "assignee", "story", "daily"}, store.sets)
}

func TestContract_Unauthorized(t *testing.T) {
	provider := preferences.NewLocalProvider(&fakeUserStore{}, zerolog.Nop())
	srv := preferences.NewServer(provider, "right-token", zerolog.Nop())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	client, err := preferencesclient.New(ts.URL, "wrong-token", nil)
	require.NoError(t, err)

	err = client.SetLanguage(context.Background(), 1, "en")
	require.Error(t, err)
	var cerr *preferencesclient.Error
	require.True(t, errors.As(err, &cerr))
	assert.Equal(t, 401, cerr.Status)
	assert.Equal(t, preferencesv1.ErrCodeUnauthorized, cerr.Code)
}

func TestContract_InvalidRequest(t *testing.T) {
	provider := preferences.NewLocalProvider(&fakeUserStore{}, zerolog.Nop())
	srv := preferences.NewServer(provider, "", zerolog.Nop())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	client, err := preferencesclient.New(ts.URL, "", nil)
	require.NoError(t, err)

	err = client.SetLanguage(context.Background(), 0, "en")
	require.Error(t, err)
	var cerr *preferencesclient.Error
	require.True(t, errors.As(err, &cerr))
	assert.Equal(t, 400, cerr.Status)
	assert.Equal(t, preferencesv1.ErrCodeInvalidRequest, cerr.Code)
}
