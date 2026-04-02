package notifydedup

import (
	"sync"
	"time"
)

// Guard prevents the same notification (chatID + issueKey) from being
// sent more than once within a configurable time window. It is safe for
// concurrent use by both the poller and webhook goroutines.
type Guard struct {
	mu     sync.Mutex
	ttl    time.Duration
	stopCh chan struct{}
	// sent maps "chatID:issueKey" → expiry timestamp.
	sent map[string]time.Time
}

// New creates a Guard with the given deduplication window.
func New(ttl time.Duration) *Guard {
	g := &Guard{
		ttl:    ttl,
		sent:   make(map[string]time.Time),
		stopCh: make(chan struct{}),
	}
	go g.cleanupLoop()
	return g
}

// Stop terminates the background cleanup goroutine.
func (g *Guard) Stop() {
	close(g.stopCh)
}

// Allow returns true if a notification for this chat+issue has NOT been
// sent recently. On true it records the event so subsequent calls within
// the TTL window return false.
func (g *Guard) Allow(chatID int64, issueKey string) bool {
	key := formatKey(chatID, issueKey)

	g.mu.Lock()
	defer g.mu.Unlock()

	if exp, ok := g.sent[key]; ok && time.Now().Before(exp) {
		return false
	}
	g.sent[key] = time.Now().Add(g.ttl)
	return true
}

func formatKey(chatID int64, issueKey string) string {
	// Avoids fmt import; simple and collision-free.
	buf := make([]byte, 0, 32)
	buf = appendInt64(buf, chatID)
	buf = append(buf, ':')
	buf = append(buf, issueKey...)
	return string(buf)
}

func appendInt64(buf []byte, n int64) []byte {
	if n < 0 {
		buf = append(buf, '-')
		n = -n
	}
	if n == 0 {
		return append(buf, '0')
	}
	var tmp [20]byte
	i := len(tmp)
	for n > 0 {
		i--
		tmp[i] = byte('0' + n%10)
		n /= 10
	}
	return append(buf, tmp[i:]...)
}

func (g *Guard) cleanupLoop() {
	ticker := time.NewTicker(g.ttl)
	defer ticker.Stop()
	for {
		select {
		case <-g.stopCh:
			return
		case <-ticker.C:
			g.mu.Lock()
			now := time.Now()
			for k, exp := range g.sent {
				if now.After(exp) {
					delete(g.sent, k)
				}
			}
			g.mu.Unlock()
		}
	}
}
