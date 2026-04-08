// Package notiflog keeps an in-memory ring buffer of the most recent
// outbound notifications plus cumulative counters (received / merged /
// sent per source) for the admin debug panel.
package notiflog

import (
	"sync"
	"sync/atomic"
	"time"
)

// Stats is a point-in-time snapshot of the cumulative counters.
type Stats struct {
	ReceivedPoller  uint64
	ReceivedWebhook uint64
	SentPoller      uint64
	SentWebhook     uint64
	Merged          uint64
	// StartedAt is when the counters were (re)initialized — lets the
	// admin see the window the totals cover.
	StartedAt time.Time
}

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

// Log is a thread-safe ring buffer of recent notifications plus
// cumulative counters.
type Log struct {
	mu      sync.Mutex
	entries []Entry
	cap     int

	startedAt time.Time

	receivedPoller  atomic.Uint64
	receivedWebhook atomic.Uint64
	sentPoller      atomic.Uint64
	sentWebhook     atomic.Uint64
	merged          atomic.Uint64
}

// New returns a Log that keeps the last `capacity` entries.
func New(capacity int) *Log {
	if capacity <= 0 {
		capacity = 10
	}
	return &Log{cap: capacity, startedAt: time.Now()}
}

// RecordReceived bumps the received counter for the given source. Call
// this at the point the notification pipeline first observes an event
// that plausibly leads to a Telegram message (after initial filters).
func (l *Log) RecordReceived(src Source) {
	if l == nil {
		return
	}
	switch src {
	case SourcePoller:
		l.receivedPoller.Add(1)
	case SourceWebhook:
		l.receivedWebhook.Add(1)
	}
}

// Snapshot returns the current counter values.
func (l *Log) Snapshot() Stats {
	if l == nil {
		return Stats{}
	}
	return Stats{
		ReceivedPoller:  l.receivedPoller.Load(),
		ReceivedWebhook: l.receivedWebhook.Load(),
		SentPoller:      l.sentPoller.Load(),
		SentWebhook:     l.sentWebhook.Load(),
		Merged:          l.merged.Load(),
		StartedAt:       l.startedAt,
	}
}

// Record appends a new entry, dropping the oldest when capacity is
// reached, and bumps the "sent" counters.
func (l *Log) Record(e Entry) {
	if l == nil {
		return
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	switch e.Source {
	case SourcePoller:
		l.sentPoller.Add(1)
	case SourceWebhook:
		l.sentWebhook.Add(1)
	}
	if e.Merged {
		l.merged.Add(1)
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
