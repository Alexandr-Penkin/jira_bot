package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"SleepJiraBot/internal/storage"
	identityv1 "SleepJiraBot/pkg/identityv1"
)

// stubTokenProvider lets us verify that the jira client asks for a lease
// and consumes its fields correctly.
type stubTokenProvider struct {
	gotReq identityv1.TokenLeaseRequest
	resp   *identityv1.TokenLeaseResponse
	err    error
	calls  int
}

func (s *stubTokenProvider) Lease(_ context.Context, req identityv1.TokenLeaseRequest) (*identityv1.TokenLeaseResponse, error) {
	s.calls++
	s.gotReq = req
	return s.resp, s.err
}

// captureRT is a RoundTripper that records the last request and returns a
// canned response, so doRequest can be exercised without a live Jira.
type captureRT struct {
	lastReq  *http.Request
	response *http.Response
	err      error
}

func (c *captureRT) RoundTrip(req *http.Request) (*http.Response, error) {
	// Drain the body so the caller can inspect it later if needed.
	if req.Body != nil {
		_, _ = io.ReadAll(req.Body)
	}
	c.lastReq = req
	if c.err != nil {
		return nil, c.err
	}
	return c.response, nil
}

func newCapturingClient(status int, body string) *captureRT {
	return &captureRT{
		response: &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(bytes.NewReader([]byte(body))),
			Header:     http.Header{"Content-Type": {"application/json"}},
		},
	}
}

// withHTTPClient swaps the package-level httpClient for the duration of a
// test and restores it on cleanup. Captured to a test helper because
// SetHTTPClient has no reset hook.
func withHTTPClient(t *testing.T, rt http.RoundTripper) {
	t.Helper()
	prev := httpClient
	httpClient = &http.Client{Transport: rt, Timeout: 5 * time.Second}
	t.Cleanup(func() { httpClient = prev })
}

func TestClient_EnsureValidToken_UsesProvider(t *testing.T) {
	stub := &stubTokenProvider{
		resp: &identityv1.TokenLeaseResponse{
			AccessToken: "lease-access",
			CloudID:     "lease-cloud",
			ExpiresAt:   time.Now().Add(time.Hour).Unix(),
		},
	}

	c := NewClient(nil, nil, zerolog.Nop())
	c.SetTokenProvider(stub)

	user := &storage.User{TelegramUserID: 42, JiraCloudID: "stored-cloud"}

	access, cloud, err := c.ensureValidToken(context.Background(), user)
	require.NoError(t, err)
	assert.Equal(t, "lease-access", access)
	assert.Equal(t, "lease-cloud", cloud)
	assert.Equal(t, 1, stub.calls)
	assert.Equal(t, int64(42), stub.gotReq.TelegramID)
	assert.Equal(t, int(tokenRefreshSkew.Seconds()), stub.gotReq.MinTTLSeconds)
}

func TestClient_EnsureValidToken_FallsBackToStoredCloudID(t *testing.T) {
	// An identity-svc running in compatibility mode may omit CloudID; the
	// client falls back to the value from the User row so the request URL
	// still resolves.
	stub := &stubTokenProvider{
		resp: &identityv1.TokenLeaseResponse{AccessToken: "a", CloudID: ""},
	}
	c := NewClient(nil, nil, zerolog.Nop())
	c.SetTokenProvider(stub)

	_, cloud, err := c.ensureValidToken(context.Background(), &storage.User{
		TelegramUserID: 1,
		JiraCloudID:    "fallback-cloud",
	})
	require.NoError(t, err)
	assert.Equal(t, "fallback-cloud", cloud)
}

func TestClient_EnsureValidToken_ProviderErrorPropagates(t *testing.T) {
	stub := &stubTokenProvider{err: errors.New("provider down")}
	c := NewClient(nil, nil, zerolog.Nop())
	c.SetTokenProvider(stub)

	_, _, err := c.ensureValidToken(context.Background(), &storage.User{TelegramUserID: 1})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token lease")
	assert.Contains(t, err.Error(), "provider down")
}

func TestClient_DoRequest_SetsBearerAndCloudIDFromLease(t *testing.T) {
	// End-to-end: the doRequest hook must include the leased access token
	// in Authorization and embed the leased cloud ID in the URL.
	rt := newCapturingClient(200, `{"accountId":"acc-1"}`)
	withHTTPClient(t, rt)

	stub := &stubTokenProvider{
		resp: &identityv1.TokenLeaseResponse{
			AccessToken: "leased-token",
			CloudID:     "cloud-xyz",
		},
	}
	c := NewClient(nil, nil, zerolog.Nop())
	c.SetTokenProvider(stub)

	user := &storage.User{TelegramUserID: 7, JiraCloudID: "stored"}
	body, err := c.doRequest(context.Background(), user, http.MethodGet, "/myself", nil)
	require.NoError(t, err)
	assert.Contains(t, string(body), "acc-1")

	require.NotNil(t, rt.lastReq)
	assert.Equal(t, "Bearer leased-token", rt.lastReq.Header.Get("Authorization"))
	assert.True(t, strings.Contains(rt.lastReq.URL.Path, "/ex/jira/cloud-xyz/"),
		"cloud id from lease must be embedded in URL, got %q", rt.lastReq.URL.Path)
}

func TestClient_DoRequest_HTTPErrorReturned(t *testing.T) {
	rt := newCapturingClient(403, `{"error":"forbidden"}`)
	withHTTPClient(t, rt)

	stub := &stubTokenProvider{
		resp: &identityv1.TokenLeaseResponse{AccessToken: "t", CloudID: "c"},
	}
	c := NewClient(nil, nil, zerolog.Nop())
	c.SetTokenProvider(stub)

	_, err := c.doRequest(context.Background(), &storage.User{TelegramUserID: 1}, http.MethodGet, "/issue/FOO-1", nil)
	require.Error(t, err)

	var httpErr *HTTPError
	require.True(t, errors.As(err, &httpErr))
	assert.Equal(t, 403, httpErr.Status)
	assert.Equal(t, "/issue/FOO-1", httpErr.Path)
	assert.Contains(t, httpErr.Body, "forbidden")
}

func TestClient_DoRequest_ProviderErrorWrapped(t *testing.T) {
	// Without a working http client we rely on the provider erroring out
	// before any outbound request is attempted.
	stub := &stubTokenProvider{err: errors.New("lease-svc 503")}
	c := NewClient(nil, nil, zerolog.Nop())
	c.SetTokenProvider(stub)

	_, err := c.doRequest(context.Background(), &storage.User{TelegramUserID: 1}, http.MethodGet, "/myself", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ensure valid token")
	assert.Contains(t, err.Error(), "lease-svc 503")
}

func TestHTTPError_ErrorFormat(t *testing.T) {
	e := &HTTPError{Method: http.MethodPost, Path: "/issue", Status: 400, Body: "bad"}
	got := e.Error()
	assert.Contains(t, got, "POST")
	assert.Contains(t, got, "/issue")
	assert.Contains(t, got, "400")
	assert.Contains(t, got, "bad")
}

func TestClient_SetEventPublisher_NilResetsToNoop(t *testing.T) {
	// SetEventPublisher(nil) must not leave the client with a nil publisher —
	// otherwise later Publish calls panic. It falls back to the Noop default.
	c := NewClient(nil, nil, zerolog.Nop())
	c.SetEventPublisher(nil)

	// Sanity-check by attempting a Publish via the noop (nothing we can
	// observe; absence of panic is the assertion).
	assert.NotNil(t, c.pub)
}

func TestStoryPointsQueryFields_UserConfiguredField(t *testing.T) {
	got := storyPointsQueryFields("customfield_99999")
	// Must include the user's custom field and must not add auto-detect fallbacks.
	assert.Contains(t, got, "customfield_99999")
	assert.NotContains(t, got, "story_points,")
	assert.NotContains(t, got, "customfield_10016")
}

func TestStoryPointsQueryFields_DefaultIncludesAllCandidates(t *testing.T) {
	got := storyPointsQueryFields("")
	for _, name := range []string{"story_points", "story_point_estimate", "customfield_10016"} {
		assert.Contains(t, got, name, "missing fallback field %q", name)
	}
}

func TestExtractStoryPoints_SpecificField(t *testing.T) {
	fields := mustFields(t, `{"customfield_42": 13}`)
	got := extractStoryPoints(fields, "customfield_42")
	if assert.NotNil(t, got) {
		assert.InDelta(t, 13.0, *got, 0.0001)
	}
}

func TestExtractStoryPoints_SpecificFieldMissing(t *testing.T) {
	fields := mustFields(t, `{"other": 1}`)
	assert.Nil(t, extractStoryPoints(fields, "customfield_42"))
}

func TestExtractStoryPoints_SpecificFieldNull(t *testing.T) {
	fields := mustFields(t, `{"customfield_42": null}`)
	assert.Nil(t, extractStoryPoints(fields, "customfield_42"))
}

func TestExtractStoryPoints_FallbackOrder(t *testing.T) {
	// story_points present → used.
	fields := mustFields(t, `{"story_points": 3, "customfield_10016": 99}`)
	got := extractStoryPoints(fields, "")
	if assert.NotNil(t, got) {
		assert.InDelta(t, 3.0, *got, 0.0001)
	}

	// story_points missing but story_point_estimate present → used.
	fields = mustFields(t, `{"story_point_estimate": 5, "customfield_10016": 99}`)
	got = extractStoryPoints(fields, "")
	if assert.NotNil(t, got) {
		assert.InDelta(t, 5.0, *got, 0.0001)
	}

	// All preferred missing → falls back to customfield_10016.
	fields = mustFields(t, `{"customfield_10016": 8}`)
	got = extractStoryPoints(fields, "")
	if assert.NotNil(t, got) {
		assert.InDelta(t, 8.0, *got, 0.0001)
	}

	// None present → nil.
	fields = mustFields(t, `{"other": 1}`)
	assert.Nil(t, extractStoryPoints(fields, ""))
}

func TestExtractUserField(t *testing.T) {
	fields := mustFields(t, `{"customfield_42":{"accountId":"acc-1","displayName":"Alice"}}`)
	got := extractUserField(fields, "customfield_42")
	if assert.NotNil(t, got) {
		assert.Equal(t, "acc-1", got.AccountID)
		assert.Equal(t, "Alice", got.DisplayName)
	}
}

func TestExtractUserField_MissingOrNull(t *testing.T) {
	assert.Nil(t, extractUserField(mustFields(t, `{}`), "customfield_42"))
	assert.Nil(t, extractUserField(mustFields(t, `{"customfield_42":null}`), "customfield_42"))
	// AccountID empty → treated as not-found.
	assert.Nil(t, extractUserField(mustFields(t, `{"customfield_42":{"displayName":"x"}}`), "customfield_42"))
}

func mustFields(t *testing.T, s string) map[string]json.RawMessage {
	t.Helper()
	var out map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(s), &out))
	return out
}
