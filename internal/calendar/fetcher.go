package calendar

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultMaxBytes caps the response body read from a calendar host.
// Google ICS exports with hundreds of events come in well under 3 MB,
// so 10 MB is a generous ceiling against runaway feeds.
const DefaultMaxBytes int64 = 10 << 20

// DefaultFetchTimeout is the HTTP-side timeout applied per fetch.
const DefaultFetchTimeout = 30 * time.Second

// FetchError carries enough context to surface a useful "could not
// fetch your calendar" message back to the user without exposing the
// full response body.
type FetchError struct {
	URL     string
	Status  int
	Snippet string
}

func (e *FetchError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("calendar fetch %s: status %d: %s", e.URL, e.Status, e.Snippet)
	}
	return fmt.Sprintf("calendar fetch %s: %s", e.URL, e.Snippet)
}

// Fetcher pulls ICS bodies. The HTTP client is intentionally separate
// from the shared Jira client so a slow calendar host does not stall
// Jira token refreshes.
type Fetcher struct {
	client   *http.Client
	maxBytes int64
}

// NewFetcher constructs a Fetcher with a dedicated client. When
// timeout==0, DefaultFetchTimeout is used. When maxBytes<=0,
// DefaultMaxBytes is used.
func NewFetcher(timeout time.Duration, maxBytes int64) *Fetcher {
	if timeout <= 0 {
		timeout = DefaultFetchTimeout
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	return &Fetcher{
		client:   &http.Client{Timeout: timeout},
		maxBytes: maxBytes,
	}
}

// NewFetcherWithClient wraps a caller-provided client. Used by tests to
// inject an httptest.Server transport.
func NewFetcherWithClient(client *http.Client, maxBytes int64) *Fetcher {
	if client == nil {
		client = &http.Client{Timeout: DefaultFetchTimeout}
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	return &Fetcher{client: client, maxBytes: maxBytes}
}

// Fetch GETs the ICS payload at url. The response is accepted when the
// Content-Type contains "text/calendar" OR the first non-blank line is
// "BEGIN:VCALENDAR" — Apple/Outlook occasionally serve ICS with an
// application/octet-stream content-type, so we sniff the body.
func (f *Fetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, &FetchError{URL: url, Snippet: err.Error()}
	}
	req.Header.Set("Accept", "text/calendar, text/plain;q=0.9, */*;q=0.5")
	req.Header.Set("User-Agent", "SleepJiraBot-Calendar/1.0")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, &FetchError{URL: url, Snippet: err.Error()}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBytes))
	if err != nil {
		return nil, &FetchError{URL: url, Status: resp.StatusCode, Snippet: "read body: " + err.Error()}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &FetchError{
			URL:     url,
			Status:  resp.StatusCode,
			Snippet: trimSnippet(body),
		}
	}

	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if !strings.Contains(ct, "text/calendar") && !looksLikeICS(body) {
		return nil, &FetchError{
			URL:     url,
			Status:  resp.StatusCode,
			Snippet: fmt.Sprintf("unexpected content-type %q and body does not begin with BEGIN:VCALENDAR", ct),
		}
	}

	return body, nil
}

func looksLikeICS(body []byte) bool {
	scanner := bytes.NewReader(body)
	buf := make([]byte, 0, 64)
	tmp := make([]byte, 1)
	for {
		n, err := scanner.Read(tmp)
		if n == 0 || err != nil {
			break
		}
		c := tmp[0]
		if c == '\n' || c == '\r' {
			if len(buf) > 0 {
				break
			}
			continue
		}
		if c == ' ' || c == '\t' {
			if len(buf) == 0 {
				continue
			}
		}
		buf = append(buf, c)
		if len(buf) >= 64 {
			break
		}
	}
	return strings.HasPrefix(strings.ToUpper(string(buf)), "BEGIN:VCALENDAR")
}

func trimSnippet(body []byte) string {
	const max = 200
	s := strings.TrimSpace(string(body))
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
