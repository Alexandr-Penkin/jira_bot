package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewRateLimiter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rl := NewRateLimiter(10, 10, time.Minute, ctx)
	assert.NotNil(t, rl)
	assert.Equal(t, 10, rl.rate)
	assert.Equal(t, 10, rl.burst)
}

func TestAllow_FirstRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rl := NewRateLimiter(10, 5, time.Minute, ctx)
	assert.True(t, rl.allow("192.168.1.1"))
}

func TestAllow_BurstExhausted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rl := NewRateLimiter(10, 3, time.Minute, ctx)

	// First 3 requests should succeed (burst = 3)
	assert.True(t, rl.allow("10.0.0.1"))
	assert.True(t, rl.allow("10.0.0.1"))
	assert.True(t, rl.allow("10.0.0.1"))

	// 4th request should be denied
	assert.False(t, rl.allow("10.0.0.1"))
}

func TestAllow_DifferentIPs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rl := NewRateLimiter(10, 1, time.Minute, ctx)

	assert.True(t, rl.allow("10.0.0.1"))
	assert.False(t, rl.allow("10.0.0.1"))

	// Different IP should have its own bucket
	assert.True(t, rl.allow("10.0.0.2"))
}

func TestAllow_WindowReset(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rl := NewRateLimiter(10, 2, 50*time.Millisecond, ctx)

	assert.True(t, rl.allow("10.0.0.1"))
	assert.True(t, rl.allow("10.0.0.1"))
	assert.False(t, rl.allow("10.0.0.1"))

	// Wait for window to elapse
	time.Sleep(60 * time.Millisecond)

	assert.True(t, rl.allow("10.0.0.1"))
}

func TestExtractIP(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"ip with port", "192.168.1.1:8080", "192.168.1.1"},
		{"ipv6 with port", "[::1]:8080", "::1"},
		{"ip without port", "192.168.1.1", "192.168.1.1"},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, extractIP(tt.input))
		})
	}
}

func TestWrap_AllowsRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rl := NewRateLimiter(10, 10, time.Minute, ctx)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := rl.Wrap(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "192.168.1.1:1234"
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestWrap_BlocksWhenRateLimited(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rl := NewRateLimiter(1, 1, time.Minute, ctx)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := rl.Wrap(handler)

	// First request succeeds
	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req1.RemoteAddr = "10.0.0.1:1234"
	w1 := httptest.NewRecorder()
	wrapped.ServeHTTP(w1, req1)
	assert.Equal(t, http.StatusOK, w1.Code)

	// Second request is rate limited
	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req2.RemoteAddr = "10.0.0.1:5678"
	w2 := httptest.NewRecorder()
	wrapped.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusTooManyRequests, w2.Code)
}

func TestWrapFunc(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rl := NewRateLimiter(10, 10, time.Minute, ctx)

	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}

	wrapped := rl.WrapFunc(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "192.168.1.1:1234"
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestCleanup_RemovesStaleVisitors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	window := 50 * time.Millisecond
	rl := NewRateLimiter(10, 10, window, ctx)

	rl.allow("10.0.0.1")

	rl.mu.Lock()
	assert.Len(t, rl.visitors, 1)
	rl.mu.Unlock()

	// Wait for cleanup to run (window * 2 + margin)
	time.Sleep(window*3 + 20*time.Millisecond)

	rl.mu.Lock()
	assert.Len(t, rl.visitors, 0)
	rl.mu.Unlock()
}
