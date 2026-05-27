package jira

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
)

func TestHandleHealth(t *testing.T) {
	cs := &CallbackServer{log: zerolog.Nop()}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	cs.handleHealth(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "ok", w.Body.String())
}

func TestHandleCallback_MissingCode(t *testing.T) {
	cs := &CallbackServer{log: zerolog.Nop()}

	req := httptest.NewRequest(http.MethodGet, "/callback?state=abc", nil)
	w := httptest.NewRecorder()

	cs.handleCallback(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleCallback_MissingState(t *testing.T) {
	cs := &CallbackServer{log: zerolog.Nop()}

	req := httptest.NewRequest(http.MethodGet, "/callback?code=abc", nil)
	w := httptest.NewRecorder()

	cs.handleCallback(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleCallback_BareGETShowsStatusPage(t *testing.T) {
	// An operator pasting the callback URL into a browser hits this
	// path. We render a human-readable status page instead of a 400 so
	// the diagnostic value is clear at a glance.
	cs := &CallbackServer{log: zerolog.Nop()}

	req := httptest.NewRequest(http.MethodGet, "/callback", nil)
	w := httptest.NewRecorder()

	cs.handleCallback(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Endpoint is up")
	assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
}

func TestHandleCallback_PartialParamsStill400(t *testing.T) {
	// A real broken callback (Jira sent one of the params but not the
	// other, or someone is fuzzing) must NOT get the friendly status
	// page — it must surface as a 400 so it's logged loudly.
	cs := &CallbackServer{log: zerolog.Nop()}

	req := httptest.NewRequest(http.MethodGet, "/callback?code=abc", nil)
	w := httptest.NewRecorder()

	cs.handleCallback(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleCallback_InvalidState(t *testing.T) {
	oauth := NewOAuthClient(OAuthConfig{
		ClientID:     "id",
		ClientSecret: "secret",
		RedirectURI:  "http://localhost/callback",
	}, zerolog.Nop())

	cs := &CallbackServer{
		oauth: oauth,
		log:   zerolog.Nop(),
	}

	req := httptest.NewRequest(http.MethodGet, "/callback?code=abc&state=invalid", nil)
	w := httptest.NewRecorder()

	cs.handleCallback(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid or expired state")
}

func TestNewCallbackServer_Routes(t *testing.T) {
	oauth := NewOAuthClient(OAuthConfig{
		ClientID:     "id",
		ClientSecret: "secret",
		RedirectURI:  "http://localhost/callback",
	}, zerolog.Nop())

	cs := NewCallbackServer(context.Background(), ":0", oauth, nil, nil, zerolog.Nop())
	assert.NotNil(t, cs)
	assert.NotNil(t, cs.server)
}
