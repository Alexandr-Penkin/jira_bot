package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"golang.org/x/net/proxy"
)

// NewHTTPClient returns an *http.Client that routes traffic through the
// given proxy URL. Supported schemes: socks5, socks5h.
// If proxyURL is empty, a plain client with the given timeout is returned.
//
// When a non-empty proxyURL is provided, the returned client uses an
// alternating transport: after a timeout it automatically switches between
// the proxy transport and a direct transport for subsequent requests, so
// retries alternate between "with proxy" and "without proxy".
func NewHTTPClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	if proxyURL == "" {
		return &http.Client{Timeout: timeout}, nil
	}

	proxyTransport, err := newProxyTransport(proxyURL)
	if err != nil {
		return nil, err
	}

	directTransport := &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}

	alt := &alternatingTransport{
		proxyRT:  proxyTransport,
		directRT: directTransport,
	}
	// Start with proxy.
	alt.useProxy.Store(true)

	return &http.Client{Timeout: timeout, Transport: alt}, nil
}

func newProxyTransport(proxyURL string) (http.RoundTripper, error) {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("invalid PROXY_URL: %w", err)
	}

	switch u.Scheme {
	case "socks5", "socks5h":
		var auth *proxy.Auth
		if u.User != nil {
			auth = &proxy.Auth{User: u.User.Username()}
			if p, ok := u.User.Password(); ok {
				auth.Password = p
			}
		}
		dialer, dialErr := proxy.SOCKS5("tcp", u.Host, auth, proxy.Direct)
		if dialErr != nil {
			return nil, fmt.Errorf("failed to create SOCKS5 dialer: %w", dialErr)
		}
		contextDialer, ok := dialer.(proxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("SOCKS5 dialer does not support DialContext")
		}
		return &http.Transport{
			DialContext: contextDialer.DialContext,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q, use socks5:// or socks5h://", u.Scheme)
	}
}

// alternatingTransport flips between two underlying RoundTrippers whenever
// the active one returns a timeout error. This lets retries automatically
// try the opposite route (proxy vs direct).
type alternatingTransport struct {
	proxyRT  http.RoundTripper
	directRT http.RoundTripper
	// useProxy holds a bool: true = use proxyRT, false = use directRT.
	useProxy atomic.Bool
}

func (a *alternatingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	useProxy := a.useProxy.Load()
	rt := a.directRT
	if useProxy {
		rt = a.proxyRT
	}
	resp, err := rt.RoundTrip(req)
	if err != nil && isTimeout(err) {
		// Flip only if no concurrent request already flipped it.
		a.useProxy.CompareAndSwap(useProxy, !useProxy)
	}
	return resp, err
}

func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	return false
}
