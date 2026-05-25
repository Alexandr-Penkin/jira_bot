// Package webhookstats provides a small HTTP client for the webhook-svc
// /internal/stats endpoint. Used by admin-stats handlers in cmd/bot and
// cmd/telegram-svc when the monolith no longer embeds the webhook server
// (EMBED_WEBHOOK_SERVER=false) — the in-process Handler counter is zero
// in that mode, so admin stats must reach across to the standalone svc.
package webhookstats

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
)

const (
	// cacheTTL keeps /admin from spamming webhook-svc when the operator
	// presses the stats button repeatedly. 5s is short enough that the
	// counter still feels live and long enough to absorb a burst.
	cacheTTL = 5 * time.Second
	// requestTimeout is intentionally short — admin stats is interactive
	// and a dead webhook-svc must not make the whole panel hang.
	requestTimeout = 3 * time.Second
)

// RemoteFetcher returns a closure that fetches the in-process webhook
// event counter from a remote webhook-svc. Successful values are cached
// for cacheTTL; on error the last good value (or 0 if none) is returned,
// so a transient outage surfaces as a stale counter rather than dropping
// to zero. baseURL is the service base (e.g. http://webhook-svc:8081);
// authToken is the INTERNAL_AUTH_TOKEN shared with webhook-svc.
func RemoteFetcher(baseURL, authToken string, httpClient *http.Client, log zerolog.Logger) func() int64 {
	base := strings.TrimRight(baseURL, "/") + "/internal/stats"
	var (
		mu       sync.Mutex
		cachedAt time.Time
	)
	var cached atomic.Int64
	return func() int64 {
		mu.Lock()
		if time.Since(cachedAt) < cacheTTL {
			mu.Unlock()
			return cached.Load()
		}
		mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base, http.NoBody)
		if err != nil {
			log.Debug().Err(err).Msg("webhookstats: build request")
			return cached.Load()
		}
		if authToken != "" {
			req.Header.Set("Authorization", "Bearer "+authToken)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			log.Debug().Err(err).Str("url", base).Msg("webhookstats: fetch failed")
			return cached.Load()
		}
		defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
		if resp.StatusCode >= 400 {
			log.Debug().Int("status", resp.StatusCode).Str("url", base).Msg("webhookstats: non-2xx")
			return cached.Load()
		}
		var payload struct {
			EventsReceived int64 `json:"events_received"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&payload); err != nil {
			log.Debug().Err(err).Msg("webhookstats: decode response")
			return cached.Load()
		}
		mu.Lock()
		cachedAt = time.Now()
		mu.Unlock()
		cached.Store(payload.EventsReceived)
		return payload.EventsReceived
	}
}
