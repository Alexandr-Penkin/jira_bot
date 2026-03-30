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

func TestHandleCallback_MissingBoth(t *testing.T) {
	cs := &CallbackServer{log: zerolog.Nop()}

	req := httptest.NewRequest(http.MethodGet, "/callback", nil)
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
