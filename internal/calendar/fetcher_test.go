package calendar

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFetcher_AcceptsTextCalendar(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
		_, _ = w.Write([]byte("BEGIN:VCALENDAR\nVERSION:2.0\nEND:VCALENDAR\n"))
	}))
	defer srv.Close()

	f := NewFetcherWithClient(srv.Client(), 0)
	body, err := f.Fetch(context.Background(), srv.URL)
	require.NoError(t, err)
	require.Contains(t, string(body), "BEGIN:VCALENDAR")
}

func TestFetcher_AcceptsOctetStreamWithICSBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("BEGIN:VCALENDAR\nVERSION:2.0\nEND:VCALENDAR\n"))
	}))
	defer srv.Close()

	f := NewFetcherWithClient(srv.Client(), 0)
	body, err := f.Fetch(context.Background(), srv.URL)
	require.NoError(t, err)
	require.NotEmpty(t, body)
}

func TestFetcher_RejectsHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>not a calendar</html>"))
	}))
	defer srv.Close()

	f := NewFetcherWithClient(srv.Client(), 0)
	_, err := f.Fetch(context.Background(), srv.URL)
	require.Error(t, err)
	var fe *FetchError
	require.ErrorAs(t, err, &fe)
	require.Equal(t, 200, fe.Status)
}

func TestFetcher_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("nope"))
	}))
	defer srv.Close()

	f := NewFetcherWithClient(srv.Client(), 0)
	_, err := f.Fetch(context.Background(), srv.URL)
	require.Error(t, err)
	var fe *FetchError
	require.ErrorAs(t, err, &fe)
	require.Equal(t, 404, fe.Status)
}

func TestFetcher_LimitedRead(t *testing.T) {
	big := strings.Repeat("X", 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/calendar")
		_, _ = w.Write([]byte("BEGIN:VCALENDAR\n" + big))
	}))
	defer srv.Close()

	f := NewFetcherWithClient(srv.Client(), 5)
	body, err := f.Fetch(context.Background(), srv.URL)
	require.NoError(t, err)
	require.Len(t, body, 5)
}

func TestFetcher_NetworkErrorWrapped(t *testing.T) {
	f := NewFetcherWithClient(&http.Client{Transport: failingTransport{}}, 0)
	_, err := f.Fetch(context.Background(), "https://example.invalid")
	require.Error(t, err)
	var fe *FetchError
	require.ErrorAs(t, err, &fe)
}

type failingTransport struct{}

func (failingTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	return nil, errors.New("network unreachable")
}
