package notifydedup_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"SleepJiraBot/internal/notifydedup"
)

func TestGuard_AllowFirstTime(t *testing.T) {
	g := notifydedup.New(time.Minute)
	defer g.Stop()

	assert.True(t, g.Allow(42, "ABC-1"))
}

func TestGuard_BlocksDuplicateWithinTTL(t *testing.T) {
	g := notifydedup.New(time.Minute)
	defer g.Stop()

	assert.True(t, g.Allow(42, "ABC-1"))
	assert.False(t, g.Allow(42, "ABC-1"))
	assert.False(t, g.Allow(42, "ABC-1"))
}

func TestGuard_SeparateKeysIndependent(t *testing.T) {
	g := notifydedup.New(time.Minute)
	defer g.Stop()

	assert.True(t, g.Allow(1, "ABC-1"))
	assert.True(t, g.Allow(2, "ABC-1"), "different chat ID must be independent")
	assert.True(t, g.Allow(1, "ABC-2"), "different issue must be independent")
}

func TestGuard_NegativeChatID(t *testing.T) {
	// Telegram groups use negative chat IDs — must be handled.
	g := notifydedup.New(time.Minute)
	defer g.Stop()

	assert.True(t, g.Allow(-1001, "ABC-1"))
	assert.False(t, g.Allow(-1001, "ABC-1"))
	assert.True(t, g.Allow(1001, "ABC-1"), "positive vs negative must not collide")
}

func TestGuard_ReallowsAfterTTL(t *testing.T) {
	g := notifydedup.New(10 * time.Millisecond)
	defer g.Stop()

	assert.True(t, g.Allow(7, "ABC-1"))
	assert.False(t, g.Allow(7, "ABC-1"))

	time.Sleep(25 * time.Millisecond)
	assert.True(t, g.Allow(7, "ABC-1"), "ttl expiry must re-open the key")
}

func TestGuard_ZeroChatID(t *testing.T) {
	g := notifydedup.New(time.Minute)
	defer g.Stop()

	assert.True(t, g.Allow(0, "ABC-1"))
	assert.False(t, g.Allow(0, "ABC-1"))
}

func TestGuard_ConcurrentAllowsExactlyOneWinner(t *testing.T) {
	// N goroutines race on the same key; exactly one must see Allow=true.
	g := notifydedup.New(time.Minute)
	defer g.Stop()

	var wins int32
	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if g.Allow(9, "SHARED-1") {
				atomic.AddInt32(&wins, 1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(1), atomic.LoadInt32(&wins))
}

func TestGuard_StopIsIdempotentlySafe(t *testing.T) {
	g := notifydedup.New(50 * time.Millisecond)
	// Stop while a cleanup tick may be pending; must not panic.
	g.Stop()
}
