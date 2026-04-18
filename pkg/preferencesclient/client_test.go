package preferencesclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"SleepJiraBot/pkg/preferencesclient"
	preferencesv1 "SleepJiraBot/pkg/preferencesv1"
)

func TestNew_RejectsEmptyBaseURL(t *testing.T) {
	_, err := preferencesclient.New("", "", nil)
	require.Error(t, err)
}

func TestClient_Get_Ok(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "42", r.URL.Query().Get("telegram_id"))
		assert.Equal(t, "Bearer tok", r.Header.Get(preferencesv1.AuthHeader))
		_ = json.NewEncoder(w).Encode(preferencesv1.Preferences{TelegramID: 42, Language: "ru"})
	}))
	defer ts.Close()

	c, err := preferencesclient.New(ts.URL, "tok", nil)
	require.NoError(t, err)
	got, err := c.Get(context.Background(), 42)
	require.NoError(t, err)
	assert.EqualValues(t, 42, got.TelegramID)
	assert.Equal(t, "ru", got.Language)
}

func TestClient_Get_NotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(preferencesv1.ErrorResponse{Code: preferencesv1.ErrCodeNotFound, Message: "x"})
	}))
	defer ts.Close()

	c, _ := preferencesclient.New(ts.URL, "", nil)
	_, err := c.Get(context.Background(), 42)
	require.Error(t, err)
	var cerr *preferencesclient.Error
	require.True(t, errors.As(err, &cerr))
	assert.Equal(t, http.StatusNotFound, cerr.Status)
	assert.Equal(t, preferencesv1.ErrCodeNotFound, cerr.Code)
	assert.True(t, preferencesclient.IsNotFound(err))
}

func TestClient_SetLanguage_Ok(t *testing.T) {
	var gotBody preferencesv1.SetLanguageRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, preferencesv1.SetLanguagePath, r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(body, &gotBody))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	c, _ := preferencesclient.New(ts.URL, "", nil)
	require.NoError(t, c.SetLanguage(context.Background(), 7, "en"))
	assert.EqualValues(t, 7, gotBody.TelegramID)
	assert.Equal(t, "en", gotBody.Language)
}

func TestClient_SetDefaults_PropagatesError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(preferencesv1.ErrorResponse{Code: preferencesv1.ErrCodeInvalidRequest, Message: "bad"})
	}))
	defer ts.Close()

	c, _ := preferencesclient.New(ts.URL, "", nil)
	err := c.SetDefaults(context.Background(), 7, "PROJ", 3)
	require.Error(t, err)
	var cerr *preferencesclient.Error
	require.True(t, errors.As(err, &cerr))
	assert.Equal(t, http.StatusBadRequest, cerr.Status)
	assert.Equal(t, preferencesv1.ErrCodeInvalidRequest, cerr.Code)
}

func TestClient_AllSetters_HitExpectedPaths(t *testing.T) {
	var paths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	c, _ := preferencesclient.New(ts.URL, "", nil)
	ctx := context.Background()
	require.NoError(t, c.SetSprintIssueTypes(ctx, 1, []string{"Task"}))
	require.NoError(t, c.SetDoneStatuses(ctx, 1, []string{"Done"}))
	require.NoError(t, c.SetHoldStatuses(ctx, 1, []string{"Blocked"}))
	require.NoError(t, c.SetAssigneeField(ctx, 1, "cf1"))
	require.NoError(t, c.SetStoryPointsField(ctx, 1, "cf2"))
	require.NoError(t, c.SetDailyJQL(ctx, 1, "a", "b", "c"))

	want := []string{
		preferencesv1.SetSprintIssueTypesPath,
		preferencesv1.SetDoneStatusesPath,
		preferencesv1.SetHoldStatusesPath,
		preferencesv1.SetAssigneeFieldPath,
		preferencesv1.SetStoryPointsFieldPath,
		preferencesv1.SetDailyJQLPath,
	}
	assert.Equal(t, want, paths)
}

func TestClient_TrimsTrailingSlash(t *testing.T) {
	var gotURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	c, _ := preferencesclient.New(ts.URL+"/", "", nil)
	require.NoError(t, c.SetLanguage(context.Background(), 1, "en"))
	assert.True(t, strings.HasPrefix(gotURL, preferencesv1.SetLanguagePath))
}
