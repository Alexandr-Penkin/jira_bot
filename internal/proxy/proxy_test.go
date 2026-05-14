package proxy

import (
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
	assert.NotNil(t, c.Transport, "Transport should be the otelhttp-wrapped default")
}

func TestNewHTTPClient_Socks5Succeeds(t *testing.T) {
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
