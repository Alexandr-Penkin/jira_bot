// Package notiflog keeps an in-memory ring buffer of the most recent
// outbound notifications for debugging from the admin panel.
package notiflog

import (
	"sync"
	"time"
)

// Source identifies which subsystem produced the notification.
type Source string

const (
	SourcePoller  Source = "poller"
	SourceWebhook Source = "webhook"
)

// Entry is a single recorded notification.
type Entry struct {
	At       time.Time
	Source   Source
	ChatID   int64
	IssueKey string
	IssueURL string
	// Changes is a short human summary of what changed (one line per
	// field for poller, event type for webhook).
	Changes string
	// Merged is true when multiple changes were collapsed into this
	// notification (poller batching). Always false for webhook entries.
	Merged bool
}

// Log is a thread-safe ring buffer of recent notifications.
type Log struct {
	mu      sync.Mutex
	entries []Entry
	cap     int
}

// New returns a Log that keeps the last `capacity` entries.
func New(capacity int) *Log {
	if capacity <= 0 {
		capacity = 10
	}
	return &Log{cap: capacity}
}

// Record appends a new entry, dropping the oldest when capacity is reached.
func (l *Log) Record(e Entry) {
	if l == nil {
		return
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.entries) >= l.cap {
		copy(l.entries, l.entries[1:])
		l.entries = l.entries[:len(l.entries)-1]
	}
	l.entries = append(l.entries, e)
}

// Recent returns the stored entries, newest first.
func (l *Log) Recent() []Entry {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Entry, len(l.entries))
	for i, e := range l.entries {
		out[len(l.entries)-1-i] = e
	}
	return out
}
