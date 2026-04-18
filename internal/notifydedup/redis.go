package notifydedup

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

const (
	redisKeyPrefix = "sjb:dedup:"
	redisOpTimeout = 500 * time.Millisecond
)

// RedisGuard is the distributed counterpart of Guard. A single SETNX
// with EX ttl replaces the in-memory map + expiry, so replicas of
// subscription-svc / webhook-svc converge on a single notification per
// (chatID, issueKey) window.
//
// Fail-closed: on Redis errors Allow returns false (skip notification)
// rather than true (duplicate storm). The plan §1.6C pins this choice:
// dropping a notification is recoverable; flooding a user is not.
type RedisGuard struct {
	client *redis.Client
	ttl    time.Duration
	log    zerolog.Logger
}

// NewRedis constructs a RedisGuard from a redis:// URL. Call Ping in
// the caller to fail fast at startup if the URL is wrong.
func NewRedis(redisURL string, ttl time.Duration, log zerolog.Logger) (*RedisGuard, error) {
	if redisURL == "" {
		return nil, errors.New("notifydedup: redis URL required")
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("notifydedup: parse redis URL: %w", err)
	}
	return &RedisGuard{client: redis.NewClient(opts), ttl: ttl, log: log}, nil
}

// Ping verifies the Redis connection. Call at startup.
func (g *RedisGuard) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, redisOpTimeout)
	defer cancel()
	return g.client.Ping(ctx).Err()
}

// Allow returns true when the (chatID, issueKey) slot was empty and
// this caller claimed it; false if Redis already holds the key or if
// the call errored.
func (g *RedisGuard) Allow(chatID int64, issueKey string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), redisOpTimeout)
	defer cancel()

	key := redisKeyPrefix + formatKey(chatID, issueKey)
	res, err := g.client.SetArgs(ctx, key, 1, redis.SetArgs{Mode: "NX", TTL: g.ttl}).Result()
	if err != nil {
		// redis.Nil is the library's way of reporting "NX was not
		// satisfied" — that's the normal "already deduped" path.
		if errors.Is(err, redis.Nil) {
			return false
		}
		g.log.Warn().Err(err).Str("key", key).Msg("notifydedup: redis SET NX failed; dropping notification to avoid duplicate storm")
		return false
	}
	// On success SetArgs returns "OK"; anything else is unexpected but
	// still not a claim.
	return res == "OK"
}

// Close releases the underlying Redis connection.
func (g *RedisGuard) Close() error {
	return g.client.Close()
}
