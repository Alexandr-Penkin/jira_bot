package identityclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"SleepJiraBot/pkg/identityclient"
	identityv1 "SleepJiraBot/pkg/identityv1"
)

func TestNew_RejectsEmptyBaseURL(t *testing.T) {
	_, err := identityclient.New("", "token", nil)
	require.Error(t, err)
}

func TestNew_TrimsTrailingSlash(t *testing.T) {
	// Any base URL with trailing slash must hit the lease path exactly once —
	// no "//internal/lease" duplication.
	var hits int32
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(identityv1.TokenLeaseResponse{
			AccessToken: "t",
			ExpiresAt:   time.Now().Add(time.Hour).Unix(),
		})
	}))
	defer ts.Close()

	c, err := identityclient.New(ts.URL+"/", "", nil)
	require.NoError(t, err)

	_, err = c.TokenLease(context.Background(), 1, 0)
	require.NoError(t, err)
	assert.Equal(t, identityv1.LeasePath, gotPath)
	assert.Equal(t, int32(1), atomic.LoadInt32(&hits))
}

func TestLease_SendsBearerAndReturnsResponse(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(identityv1.TokenLeaseResponse{
			AccessToken: "access-1",
			ExpiresAt:   time.Now().Add(time.Hour).Unix(),
			CloudID:     "cloud",
			SiteURL:     "https://x.atlassian.net",
		})
	}))
	defer ts.Close()

	c, err := identityclient.New(ts.URL, "secret", nil)
	require.NoError(t, err)

	resp, err := c.TokenLease(context.Background(), 42, 0)
	require.NoError(t, err)
	assert.Equal(t, "access-1", resp.AccessToken)
	assert.Equal(t, "cloud", resp.CloudID)
	assert.Equal(t, "Bearer secret", gotAuth)
}

func TestLease_NoBearerWhenTokenEmpty(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(identityv1.TokenLeaseResponse{
			AccessToken: "t",
			ExpiresAt:   time.Now().Add(time.Hour).Unix(),
		})
	}))
	defer ts.Close()

	c, err := identityclient.New(ts.URL, "", nil)
	require.NoError(t, err)

	_, err = c.TokenLease(context.Background(), 1, 0)
	require.NoError(t, err)
	assert.Empty(t, gotAuth)
}

func TestLease_ErrorResponseParsedAsLeaseError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(identityv1.ErrorResponse{
			Code:    identityv1.ErrCodeInvalidRefreshToken,
			Message: "revoked",
		})
	}))
	defer ts.Close()

	c, err := identityclient.New(ts.URL, "", nil)
	require.NoError(t, err)

	_, err = c.TokenLease(context.Background(), 1, 0)
	require.Error(t, err)

	var leaseErr *identityclient.LeaseError
	require.True(t, errors.As(err, &leaseErr))
	assert.Equal(t, http.StatusUnauthorized, leaseErr.Status)
	assert.Equal(t, identityv1.ErrCodeInvalidRefreshToken, leaseErr.Code)
	assert.Equal(t, "revoked", leaseErr.Message)
	assert.Contains(t, leaseErr.Error(), "401")
}

func TestLease_CachesFreshResponse(t *testing.T) {
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_ = json.NewEncoder(w).Encode(identityv1.TokenLeaseResponse{
			AccessToken: "cached",
			ExpiresAt:   time.Now().Add(30 * time.Minute).Unix(),
		})
	}))
	defer ts.Close()

	c, err := identityclient.New(ts.URL, "", nil)
	require.NoError(t, err)

	_, err = c.TokenLease(context.Background(), 7, 0)
	require.NoError(t, err)
	_, err = c.TokenLease(context.Background(), 7, 0)
	require.NoError(t, err)

	assert.Equal(t, int32(1), atomic.LoadInt32(&hits), "second call must hit cache")
}

func TestLease_NearExpiryForcesRefetch(t *testing.T) {
	// First response expires within the cacheSkew (60s) → second call must
	// round-trip again.
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		exp := time.Now().Add(30 * time.Second).Unix() // within skew
		if n > 1 {
			exp = time.Now().Add(time.Hour).Unix()
		}
		_ = json.NewEncoder(w).Encode(identityv1.TokenLeaseResponse{
			AccessToken: "t",
			ExpiresAt:   exp,
		})
	}))
	defer ts.Close()

	c, err := identityclient.New(ts.URL, "", nil)
	require.NoError(t, err)

	_, err = c.TokenLease(context.Background(), 3, 0)
	require.NoError(t, err)
	_, err = c.TokenLease(context.Background(), 3, 0)
	require.NoError(t, err)

	assert.Equal(t, int32(2), atomic.LoadInt32(&hits))
}

func TestLease_MinTTLForcesRefetch(t *testing.T) {
	// Response is valid for 10 minutes but caller demands 20 → cache miss.
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_ = json.NewEncoder(w).Encode(identityv1.TokenLeaseResponse{
			AccessToken: "t",
			ExpiresAt:   time.Now().Add(10 * time.Minute).Unix(),
		})
	}))
	defer ts.Close()

	c, err := identityclient.New(ts.URL, "", nil)
	require.NoError(t, err)

	_, err = c.TokenLease(context.Background(), 9, 0)
	require.NoError(t, err)
	_, err = c.TokenLease(context.Background(), 9, int((20 * time.Minute).Seconds()))
	require.NoError(t, err)

	assert.Equal(t, int32(2), atomic.LoadInt32(&hits))
}

func TestInvalidate_DropsCache(t *testing.T) {
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_ = json.NewEncoder(w).Encode(identityv1.TokenLeaseResponse{
			AccessToken: "t",
			ExpiresAt:   time.Now().Add(time.Hour).Unix(),
		})
	}))
	defer ts.Close()

	c, err := identityclient.New(ts.URL, "", nil)
	require.NoError(t, err)

	_, err = c.TokenLease(context.Background(), 5, 0)
	require.NoError(t, err)

	c.Invalidate(5)

	_, err = c.TokenLease(context.Background(), 5, 0)
	require.NoError(t, err)

	assert.Equal(t, int32(2), atomic.LoadInt32(&hits))
}

func TestLease_NetworkErrorWrapped(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	ts.Close() // connection refused

	c, err := identityclient.New(ts.URL, "", nil)
	require.NoError(t, err)

	_, err = c.TokenLease(context.Background(), 1, 0)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "call identity-svc"))
}
