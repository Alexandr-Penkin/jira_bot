package middleware

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// RateLimiter implements a simple token-bucket rate limiter per IP.
type RateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*bucket
	rate     int
	burst    int
	window   time.Duration
	log      zerolog.Logger
}

type bucket struct {
	tokens    int
	lastReset time.Time
}

// NewRateLimiter creates a rate limiter that allows `rate` requests per `window` with `burst` max.
// The provided context controls the lifetime of the background cleanup goroutine.
func NewRateLimiter(rate, burst int, window time.Duration, ctx ...context.Context) *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*bucket),
		rate:     rate,
		burst:    burst,
		window:   window,
		log:      zerolog.Nop(),
	}

	var cleanupCtx context.Context
	if len(ctx) > 0 && ctx[0] != nil {
		cleanupCtx = ctx[0]
	} else {
		cleanupCtx = context.Background()
	}

	go rl.cleanup(cleanupCtx)
	return rl
}

// SetLogger configures the logger for rate limit events.
func (rl *RateLimiter) SetLogger(log zerolog.Logger) {
	rl.log = log
}

func (rl *RateLimiter) cleanup(ctx context.Context) {
	ticker := time.NewTicker(rl.window)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rl.mu.Lock()
			for ip, b := range rl.visitors {
				if time.Since(b.lastReset) > rl.window*2 {
					delete(rl.visitors, ip)
				}
			}
			rl.mu.Unlock()
		}
	}
}

func (rl *RateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.visitors[ip]
	if !ok {
		rl.visitors[ip] = &bucket{
			tokens:    rl.burst - 1,
			lastReset: now,
		}
		return true
	}

	elapsed := now.Sub(b.lastReset)
	if elapsed >= rl.window {
		b.tokens = rl.burst
		b.lastReset = now
	} else {
		refill := int(elapsed.Seconds() / rl.window.Seconds() * float64(rl.rate))
		b.tokens += refill
		if b.tokens > rl.burst {
			b.tokens = rl.burst
		}
	}

	if b.tokens <= 0 {
		return false
	}

	b.tokens--
	return true
}

// Wrap wraps an http.Handler with rate limiting.
// Uses only RemoteAddr for IP extraction to prevent X-Forwarded-For spoofing.
func (rl *RateLimiter) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := extractIP(r.RemoteAddr)

		if !rl.allow(ip) {
			rl.log.Debug().Str("ip", ip).Msg("rate limit exceeded")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// extractIP extracts the IP address from RemoteAddr, stripping the port.
func extractIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

// WrapFunc wraps an http.HandlerFunc with rate limiting.
func (rl *RateLimiter) WrapFunc(next http.HandlerFunc) http.Handler {
	return rl.Wrap(next)
}
