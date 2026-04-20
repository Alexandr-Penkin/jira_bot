// Package identityclient provides an HTTP client for the Phase-2
// identity-svc TokenLease endpoint. A Client instance is safe for
// concurrent use and caches responses until (ExpiresAt − refreshSkew).
package identityclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	identityv1 "SleepJiraBot/pkg/identityv1"
)

// DefaultTimeout bounds a single lease HTTP call. Kept short: the
// server-side refresh path hits Atlassian, which is already bounded
// inside the provider; at the wire layer we want failure to surface
// quickly so callers can back off.
const DefaultTimeout = 10 * time.Second

// cacheSkew matches the server-side refreshSkew. A cached entry whose
// ExpiresAt is less than `cacheSkew` away is considered stale and the
// client calls the server again. This prevents a race where the cache
// hands out a token that expires mid-request.
const cacheSkew = 60 * time.Second

// Client talks to identity-svc over HTTP. The base URL must include
// scheme and host (e.g. "http://identity-svc:9080"); the lease path is
// appended internally.
type Client struct {
	baseURL   string
	authToken string
	http      *http.Client

	mu    sync.Mutex
	cache map[int64]*identityv1.TokenLeaseResponse
}

// New constructs a Client. When httpClient is nil, a client with
// DefaultTimeout and no proxy is used. The authToken is sent as a
// bearer credential — pass "" to call an unauthenticated server.
func New(baseURL, authToken string, httpClient *http.Client) (*Client, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("identityclient: baseURL required")
	}
	if _, err := url.Parse(baseURL); err != nil {
		return nil, fmt.Errorf("identityclient: invalid baseURL: %w", err)
	}
	if httpClient == nil {
		// Wrap the default transport with otelhttp so identity-svc calls
		// show up as child spans of the caller's request context and
		// surface in the otelhttp client-duration histogram. Callers that
		// pass their own client own their instrumentation.
		httpClient = &http.Client{
			Timeout:   DefaultTimeout,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		}
	}
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		authToken: authToken,
		http:      httpClient,
		cache:     make(map[int64]*identityv1.TokenLeaseResponse),
	}, nil
}

// Lease returns an access token for the given Telegram user. When a
// cached entry exists and has more than cacheSkew of life left, it is
// returned without a network call. req.MinTTLSeconds is forwarded to
// the server; 0 means "any fresh-enough token is fine". The signature
// matches identity.LocalProvider.Lease so the two implementations are
// interchangeable behind a TokenProvider interface.
func (c *Client) Lease(ctx context.Context, req identityv1.TokenLeaseRequest) (*identityv1.TokenLeaseResponse, error) {
	if cached, ok := c.lookup(req.TelegramID, req.MinTTLSeconds); ok {
		return cached, nil
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+identityv1.LeasePath, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.authToken != "" {
		httpReq.Header.Set(identityv1.AuthHeader, identityv1.AuthScheme+c.authToken)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call identity-svc: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, fmt.Errorf("read identity-svc response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errResp identityv1.ErrorResponse
		_ = json.Unmarshal(raw, &errResp)
		return nil, &LeaseError{
			Status:  resp.StatusCode,
			Code:    errResp.Code,
			Message: errResp.Message,
		}
	}

	var parsed identityv1.TokenLeaseResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode identity-svc response: %w", err)
	}

	c.store(req.TelegramID, &parsed)
	return &parsed, nil
}

// TokenLease is a convenience wrapper for callers that just want to
// pass telegramID + minTTLSeconds without constructing a request
// struct.
func (c *Client) TokenLease(ctx context.Context, telegramID int64, minTTLSeconds int) (*identityv1.TokenLeaseResponse, error) {
	return c.Lease(ctx, identityv1.TokenLeaseRequest{TelegramID: telegramID, MinTTLSeconds: minTTLSeconds})
}

// Invalidate drops any cached lease for the given user. Call when the
// downstream Jira API returns 401, so the next TokenLease round-trips
// for a fresh token.
func (c *Client) Invalidate(telegramID int64) {
	c.mu.Lock()
	delete(c.cache, telegramID)
	c.mu.Unlock()
}

func (c *Client) lookup(telegramID int64, minTTLSeconds int) (*identityv1.TokenLeaseResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.cache[telegramID]
	if !ok {
		return nil, false
	}
	skew := cacheSkew
	if minTTLSeconds > int(cacheSkew.Seconds()) {
		skew = time.Duration(minTTLSeconds) * time.Second
	}
	if time.Unix(entry.ExpiresAt, 0).Before(time.Now().Add(skew)) {
		return nil, false
	}
	cp := *entry
	return &cp, true
}

func (c *Client) store(telegramID int64, resp *identityv1.TokenLeaseResponse) {
	c.mu.Lock()
	cp := *resp
	c.cache[telegramID] = &cp
	c.mu.Unlock()
}

// LeaseError is returned for any non-2xx response from identity-svc.
// Callers can react to Code (ErrCode* from identityv1) to decide
// whether to back off (refresh_failed), prompt reconnect
// (invalid_refresh_token / not_connected), or retry.
type LeaseError struct {
	Status  int
	Code    string
	Message string
}

func (e *LeaseError) Error() string {
	return fmt.Sprintf("identity-svc %d %s: %s", e.Status, e.Code, e.Message)
}
