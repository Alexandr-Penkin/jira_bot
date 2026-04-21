package preferences_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"SleepJiraBot/internal/preferences"
	preferencesv1 "SleepJiraBot/pkg/preferencesv1"
)

type stubProvider struct {
	getResp *preferencesv1.Preferences
	getErr  error
	lang    struct {
		id   int64
		lang string
	}
	defaults struct {
		id      int64
		project string
		board   int
	}
	sprintTypes struct {
		id    int64
		types []string
	}
	err error
}

func (s *stubProvider) Get(_ context.Context, id int64) (*preferencesv1.Preferences, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.getResp == nil {
		return &preferencesv1.Preferences{TelegramID: id}, nil
	}
	return s.getResp, nil
}

func (s *stubProvider) SetLanguage(_ context.Context, id int64, lang string) error {
	s.lang.id = id
	s.lang.lang = lang
	return s.err
}

func (s *stubProvider) SetDefaults(_ context.Context, id int64, project string, board int) error {
	s.defaults.id = id
	s.defaults.project = project
	s.defaults.board = board
	return s.err
}

func (s *stubProvider) SetSprintIssueTypes(_ context.Context, id int64, types []string) error {
	s.sprintTypes.id = id
	s.sprintTypes.types = types
	return s.err
}

func (s *stubProvider) SetDefaultIssueType(_ context.Context, _ int64, _, _ string) error {
	return s.err
}
func (s *stubProvider) SetDoneStatuses(_ context.Context, _ int64, _ []string) error   { return s.err }
func (s *stubProvider) SetHoldStatuses(_ context.Context, _ int64, _ []string) error   { return s.err }
func (s *stubProvider) SetAssigneeField(_ context.Context, _ int64, _ string) error    { return s.err }
func (s *stubProvider) SetStoryPointsField(_ context.Context, _ int64, _ string) error { return s.err }
func (s *stubProvider) SetDailyJQL(_ context.Context, _ int64, _, _, _ string) error   { return s.err }

func newTestServer(t *testing.T, prov preferences.Provider, token string) *httptest.Server {
	t.Helper()
	srv := preferences.NewServer(prov, token, zerolog.Nop())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func postJSON(t *testing.T, url, token string, body any) *http.Response {
	t.Helper()
	buf, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(buf))
	require.NoError(t, err)
	if token != "" {
		req.Header.Set(preferencesv1.AuthHeader, preferencesv1.AuthScheme+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func getWithToken(t *testing.T, url, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	if token != "" {
		req.Header.Set(preferencesv1.AuthHeader, preferencesv1.AuthScheme+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func TestServer_Healthz(t *testing.T) {
	ts := newTestServer(t, &stubProvider{}, "")
	resp, err := http.Get(ts.URL + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestServer_Get_Ok(t *testing.T) {
	prov := &stubProvider{getResp: &preferencesv1.Preferences{TelegramID: 42, Language: "ru"}}
	ts := newTestServer(t, prov, "tok")
	resp := getWithToken(t, ts.URL+preferencesv1.GetPath+"?telegram_id=42", "tok")
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out preferencesv1.Preferences
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.EqualValues(t, 42, out.TelegramID)
	assert.Equal(t, "ru", out.Language)
}

func TestServer_Get_Unauthorized(t *testing.T) {
	ts := newTestServer(t, &stubProvider{}, "tok")
	resp, err := http.Get(ts.URL + preferencesv1.GetPath + "?telegram_id=42")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestServer_Get_WrongMethod(t *testing.T) {
	ts := newTestServer(t, &stubProvider{}, "")
	resp, err := http.Post(ts.URL+preferencesv1.GetPath, "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

func TestServer_Get_MissingTelegramID(t *testing.T) {
	ts := newTestServer(t, &stubProvider{}, "")
	resp, err := http.Get(ts.URL + preferencesv1.GetPath)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestServer_Get_NotFound(t *testing.T) {
	prov := &stubProvider{getErr: &preferences.ProviderError{Code: preferencesv1.ErrCodeNotFound, Message: "x"}}
	ts := newTestServer(t, prov, "")
	resp, err := http.Get(ts.URL + preferencesv1.GetPath + "?telegram_id=42")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestServer_Get_InternalError(t *testing.T) {
	prov := &stubProvider{getErr: errors.New("boom")}
	ts := newTestServer(t, prov, "")
	resp, err := http.Get(ts.URL + preferencesv1.GetPath + "?telegram_id=42")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestServer_SetLanguage_Ok(t *testing.T) {
	prov := &stubProvider{}
	ts := newTestServer(t, prov, "tok")
	resp := postJSON(t, ts.URL+preferencesv1.SetLanguagePath, "tok", preferencesv1.SetLanguageRequest{TelegramID: 7, Language: "en"})
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.EqualValues(t, 7, prov.lang.id)
	assert.Equal(t, "en", prov.lang.lang)
}

func TestServer_SetLanguage_Unauthorized(t *testing.T) {
	prov := &stubProvider{}
	ts := newTestServer(t, prov, "tok")
	resp := postJSON(t, ts.URL+preferencesv1.SetLanguagePath, "wrong", preferencesv1.SetLanguageRequest{TelegramID: 7, Language: "en"})
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestServer_SetLanguage_BadJSON(t *testing.T) {
	ts := newTestServer(t, &stubProvider{}, "")
	resp, err := http.Post(ts.URL+preferencesv1.SetLanguagePath, "application/json", bytes.NewReader([]byte("not json")))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestServer_SetLanguage_MethodNotAllowed(t *testing.T) {
	ts := newTestServer(t, &stubProvider{}, "")
	resp, err := http.Get(ts.URL + preferencesv1.SetLanguagePath)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

func TestServer_SetDefaults_Ok(t *testing.T) {
	prov := &stubProvider{}
	ts := newTestServer(t, prov, "")
	resp := postJSON(t, ts.URL+preferencesv1.SetDefaultsPath, "", preferencesv1.SetDefaultsRequest{TelegramID: 7, DefaultProject: "PROJ", DefaultBoardID: 9})
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.Equal(t, "PROJ", prov.defaults.project)
	assert.Equal(t, 9, prov.defaults.board)
}

func TestServer_SetSprintIssueTypes_Ok(t *testing.T) {
	prov := &stubProvider{}
	ts := newTestServer(t, prov, "")
	resp := postJSON(t, ts.URL+preferencesv1.SetSprintIssueTypesPath, "", preferencesv1.SetSprintIssueTypesRequest{TelegramID: 7, IssueTypes: []string{"Task"}})
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.Equal(t, []string{"Task"}, prov.sprintTypes.types)
}

func TestServer_SetLanguage_ProviderError_BadRequest(t *testing.T) {
	prov := &stubProvider{err: &preferences.ProviderError{Code: preferencesv1.ErrCodeInvalidRequest, Message: "x"}}
	ts := newTestServer(t, prov, "")
	resp := postJSON(t, ts.URL+preferencesv1.SetLanguagePath, "", preferencesv1.SetLanguageRequest{TelegramID: 7, Language: "en"})
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	var errResp preferencesv1.ErrorResponse
	require.NoError(t, json.Unmarshal(body, &errResp))
	assert.Equal(t, preferencesv1.ErrCodeInvalidRequest, errResp.Code)
}

func TestServer_AllSetterEndpoints_Respond204(t *testing.T) {
	prov := &stubProvider{}
	ts := newTestServer(t, prov, "")

	cases := []struct {
		path string
		body any
	}{
		{preferencesv1.SetDoneStatusesPath, preferencesv1.SetStatusesRequest{TelegramID: 1, Statuses: []string{"Done"}}},
		{preferencesv1.SetHoldStatusesPath, preferencesv1.SetStatusesRequest{TelegramID: 1, Statuses: []string{"Blocked"}}},
		{preferencesv1.SetAssigneeFieldPath, preferencesv1.SetFieldRequest{TelegramID: 1, FieldID: "cf1"}},
		{preferencesv1.SetStoryPointsFieldPath, preferencesv1.SetFieldRequest{TelegramID: 1, FieldID: "cf2"}},
		{preferencesv1.SetDailyJQLPath, preferencesv1.SetDailyJQLRequest{TelegramID: 1, DoneJQL: "a", DoingJQL: "b", PlanJQL: "c"}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			resp := postJSON(t, ts.URL+tc.path, "", tc.body)
			defer resp.Body.Close()
			assert.Equal(t, http.StatusNoContent, resp.StatusCode)
		})
	}
}
