package proxy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewHTTPClient_EmptyProxyReturnsPlainClient(t *testing.T) {
	c, err := NewHTTPClient("", 3*time.Second)
	require.NoError(t, err)
	require.NotNil(t, c)
	assert.Equal(t, 3*time.Second, c.Timeout)
	// Transport is wrapped with otelhttp for outbound client spans; the
	// wrapper is a no-op when the global tracer provider is no-op.
	assert.NotNil(t, c.Transport, "Transport should be the otelhttp-wrapped default")
}

func TestNewHTTPClient_Socks5Succeeds(t *testing.T) {
	// The SOCKS5 dialer doesn't verify reachability at construction time,
	// so any syntactically valid URL builds an alternatingTransport wrapped
	// by otelhttp.
	c, err := NewHTTPClient("socks5://127.0.0.1:1080", 5*time.Second)
	require.NoError(t, err)
	assert.NotNil(t, c.Transport)
}

func TestNewHTTPClient_Socks5WithCredentials(t *testing.T) {
	c, err := NewHTTPClient("socks5://user:pass@127.0.0.1:1080", time.Second)
	require.NoError(t, err)
	assert.NotNil(t, c.Transport)
}

func TestNewHTTPClient_UnsupportedScheme(t *testing.T) {
	_, err := NewHTTPClient("http://proxy:3128", time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported proxy scheme")
}

func TestNewHTTPClient_InvalidURL(t *testing.T) {
	_, err := NewHTTPClient("://bad", time.Second)
	require.Error(t, err)
}

func TestIsTimeout_Nil(t *testing.T) {
	assert.False(t, isTimeout(nil))
}

func TestIsTimeout_DeadlineExceeded(t *testing.T) {
	assert.True(t, isTimeout(context.DeadlineExceeded))
}

type fakeNetError struct{ to bool }

func (e *fakeNetError) Error() string { return "fake" }
func (e *fakeNetError) Timeout() bool { return e.to }
func (e *fakeNetError) Temporary() bool {
	return false
}

func TestIsTimeout_NetErrorTimeout(t *testing.T) {
	assert.True(t, isTimeout(&fakeNetError{to: true}))
	assert.False(t, isTimeout(&fakeNetError{to: false}))
}

func TestIsTimeout_UnrelatedError(t *testing.T) {
	assert.False(t, isTimeout(errors.New("connection refused")))
}

// stubRT is a RoundTripper that returns either a canned response or a
// predefined error, so we can drive the alternatingTransport state machine
// deterministically.
type stubRT struct {
	err error
	hit int
}

func (s *stubRT) RoundTrip(_ *http.Request) (*http.Response, error) {
	s.hit++
	if s.err != nil {
		return nil, s.err
	}
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
}

func TestAlternatingTransport_FlipsOnTimeout(t *testing.T) {
	timeoutErr := &net.OpError{Op: "dial", Err: &fakeNetError{to: true}}

	proxyRT := &stubRT{err: timeoutErr}
	directRT := &stubRT{err: errors.New("direct-err")}

	alt := &alternatingTransport{proxyRT: proxyRT, directRT: directRT}
	alt.useProxy.Store(true)

	req, _ := http.NewRequest(http.MethodGet, "http://example.test", nil)

	// First call: proxy path, timeout → must flip the switch. stubRT
	// returns (nil, err) so no body to close; the explicit assertion
	// below documents that contract for the linter.
	//nolint:bodyclose // stubRT returns nil response on error
	resp, err := alt.RoundTrip(req)
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, 1, proxyRT.hit)
	assert.False(t, alt.useProxy.Load(), "timeout must flip to direct")

	// Second call: now hits direct path (non-timeout error, no flip).
	//nolint:bodyclose // stubRT returns nil response on error
	resp, err = alt.RoundTrip(req)
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, 1, directRT.hit)
	assert.False(t, alt.useProxy.Load(), "non-timeout must not flip")
}

func TestAlternatingTransport_NoFlipOnSuccess(t *testing.T) {
	// RoundTrip with nil error must not toggle the route.
	proxyRT := &stubRT{}
	directRT := &stubRT{}

	alt := &alternatingTransport{proxyRT: proxyRT, directRT: directRT}
	alt.useProxy.Store(true)

	req, _ := http.NewRequest(http.MethodGet, "http://example.test", nil)
	resp, err := alt.RoundTrip(req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	_ = resp.Body.Close()
	assert.True(t, alt.useProxy.Load())
}
