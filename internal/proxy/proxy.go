package proxy

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"golang.org/x/net/proxy"
)

// NewHTTPClient returns an *http.Client that routes traffic through the
// given proxy URL. Supported schemes: socks5, socks5h.
// If proxyURL is empty, a plain client with the given timeout is returned.
//
// All requests are forced through the proxy — there is no direct-route
// fallback. A failing proxy surfaces as a request error rather than
// silently leaking traffic via the host network.
func NewHTTPClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	if proxyURL == "" {
		return &http.Client{Timeout: timeout, Transport: otelhttp.NewTransport(http.DefaultTransport)}, nil
	}

	proxyTransport, err := newProxyTransport(proxyURL)
	if err != nil {
		return nil, err
	}

	return &http.Client{Timeout: timeout, Transport: otelhttp.NewTransport(proxyTransport)}, nil
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
