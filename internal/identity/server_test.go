package identity_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"SleepJiraBot/internal/identity"
	"SleepJiraBot/pkg/identityclient"
	identityv1 "SleepJiraBot/pkg/identityv1"
)

type stubProvider struct {
	resp *identityv1.TokenLeaseResponse
	err  error
	last identityv1.TokenLeaseRequest
}

func (s *stubProvider) Lease(_ context.Context, req identityv1.TokenLeaseRequest) (*identityv1.TokenLeaseResponse, error) {
	s.last = req
	return s.resp, s.err
}

func TestServer_Lease_Success(t *testing.T) {
	stub := &stubProvider{resp: &identityv1.TokenLeaseResponse{
		AccessToken: "token-1",
		ExpiresAt:   time.Now().Add(30 * time.Minute).Unix(),
		CloudID:     "cloud-1",
		SiteURL:     "https://example.atlassian.net",
	}}
	srv := identity.NewServer(stub, "secret", zerolog.Nop())
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	client, err := identityclient.New(ts.URL, "secret", nil)
	require.NoError(t, err)

	resp, err := client.TokenLease(context.Background(), 42, 0)
	require.NoError(t, err)
	assert.Equal(t, "token-1", resp.AccessToken)
	assert.Equal(t, int64(42), stub.last.TelegramID)

	// Second call should hit the cache and leave stub.last untouched.
	stub.last.TelegramID = 0
	resp2, err := client.TokenLease(context.Background(), 42, 0)
	require.NoError(t, err)
	assert.Equal(t, "token-1", resp2.AccessToken)
	assert.Equal(t, int64(0), stub.last.TelegramID, "cache should prevent second round-trip")
}

func TestServer_Lease_Unauthorized(t *testing.T) {
	srv := identity.NewServer(&stubProvider{}, "secret", zerolog.Nop())
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	client, err := identityclient.New(ts.URL, "wrong", nil)
	require.NoError(t, err)

	_, err = client.TokenLease(context.Background(), 42, 0)
	require.Error(t, err)
	var leaseErr *identityclient.LeaseError
	require.True(t, errors.As(err, &leaseErr), "expected *LeaseError, got %T", err)
	assert.Equal(t, 401, leaseErr.Status)
	assert.Equal(t, identityv1.ErrCodeUnauthorized, leaseErr.Code)
}

func TestServer_Lease_MethodNotAllowed(t *testing.T) {
	srv := identity.NewServer(&stubProvider{}, "", zerolog.Nop())
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+identityv1.LeasePath, nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

func TestServer_Lease_MalformedBodyIsBadRequest(t *testing.T) {
	srv := identity.NewServer(&stubProvider{}, "", zerolog.Nop())
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+identityv1.LeasePath, "application/json", strings.NewReader("not-json"))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestServer_Lease_InternalErrorForNonLeaseError(t *testing.T) {
	// A plain error (not *LeaseError) must surface as 500 internal.
	stub := &stubProvider{err: errors.New("boom")}
	srv := identity.NewServer(stub, "", zerolog.Nop())
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	client, err := identityclient.New(ts.URL, "", nil)
	require.NoError(t, err)

	_, err = client.TokenLease(context.Background(), 42, 0)
	require.Error(t, err)
	var leaseErr *identityclient.LeaseError
	require.True(t, errors.As(err, &leaseErr))
	assert.Equal(t, http.StatusInternalServerError, leaseErr.Status)
	assert.Equal(t, identityv1.ErrCodeInternal, leaseErr.Code)
}

func TestServer_Lease_InvalidRefreshMapsTo401(t *testing.T) {
	stub := &stubProvider{err: &identity.LeaseError{Code: identityv1.ErrCodeInvalidRefreshToken, Message: "revoked"}}
	srv := identity.NewServer(stub, "", zerolog.Nop())
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	client, err := identityclient.New(ts.URL, "", nil)
	require.NoError(t, err)

	_, err = client.TokenLease(context.Background(), 42, 0)
	require.Error(t, err)
	var leaseErr *identityclient.LeaseError
	require.True(t, errors.As(err, &leaseErr))
	assert.Equal(t, http.StatusUnauthorized, leaseErr.Status)
}

func TestServer_Lease_RefreshFailedMapsTo502(t *testing.T) {
	stub := &stubProvider{err: &identity.LeaseError{Code: identityv1.ErrCodeRefreshFailed, Message: "atlassian 500"}}
	srv := identity.NewServer(stub, "", zerolog.Nop())
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	client, err := identityclient.New(ts.URL, "", nil)
	require.NoError(t, err)

	_, err = client.TokenLease(context.Background(), 42, 0)
	require.Error(t, err)
	var leaseErr *identityclient.LeaseError
	require.True(t, errors.As(err, &leaseErr))
	assert.Equal(t, http.StatusBadGateway, leaseErr.Status)
}

func TestServer_Lease_InvalidRequestMapsTo400(t *testing.T) {
	stub := &stubProvider{err: &identity.LeaseError{Code: identityv1.ErrCodeInvalidRequest, Message: "telegram_id required"}}
	srv := identity.NewServer(stub, "", zerolog.Nop())
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	client, err := identityclient.New(ts.URL, "", nil)
	require.NoError(t, err)

	_, err = client.TokenLease(context.Background(), 42, 0)
	require.Error(t, err)
	var leaseErr *identityclient.LeaseError
	require.True(t, errors.As(err, &leaseErr))
	assert.Equal(t, http.StatusBadRequest, leaseErr.Status)
}

func TestServer_Healthz(t *testing.T) {
	srv := identity.NewServer(&stubProvider{}, "", zerolog.Nop())
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestServer_NoAuthTokenAllowsAll(t *testing.T) {
	// When authToken is empty the server accepts any request — operators
	// are expected to protect the listener at the network layer.
	stub := &stubProvider{resp: &identityv1.TokenLeaseResponse{AccessToken: "t", ExpiresAt: time.Now().Add(time.Hour).Unix()}}
	srv := identity.NewServer(stub, "", zerolog.Nop())
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	client, err := identityclient.New(ts.URL, "", nil)
	require.NoError(t, err)

	resp, err := client.TokenLease(context.Background(), 1, 0)
	require.NoError(t, err)
	assert.Equal(t, "t", resp.AccessToken)
}

func TestLeaseError_ErrorFormat(t *testing.T) {
	e := &identity.LeaseError{Code: identityv1.ErrCodeNotConnected, Message: "no creds"}
	assert.Equal(t, "not_connected: no creds", e.Error())
}

func TestServer_Lease_NotConnected(t *testing.T) {
	stub := &stubProvider{err: &identity.LeaseError{Code: identityv1.ErrCodeNotConnected, Message: "go reconnect"}}
	srv := identity.NewServer(stub, "", zerolog.Nop())
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	client, err := identityclient.New(ts.URL, "", nil)
	require.NoError(t, err)

	_, err = client.TokenLease(context.Background(), 42, 0)
	require.Error(t, err)
	var leaseErr *identityclient.LeaseError
	require.True(t, errors.As(err, &leaseErr))
	assert.Equal(t, identityv1.ErrCodeNotConnected, leaseErr.Code)
}
