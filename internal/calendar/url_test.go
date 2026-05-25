package calendar

import (
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeResolver struct {
	addrs map[string][]net.IP
}

func (f fakeResolver) LookupIP(host string) ([]net.IP, error) {
	ips, ok := f.addrs[host]
	if !ok {
		return nil, errors.New("no record")
	}
	return ips, nil
}

func TestNormalize_HTTPSPasses(t *testing.T) {
	resolver := fakeResolver{addrs: map[string][]net.IP{"calendar.google.com": {net.ParseIP("142.250.190.46")}}}
	got, err := Normalize("  https://calendar.google.com/ical/abc/secret.ics  ", resolver)
	require.NoError(t, err)
	require.Equal(t, "https://calendar.google.com/ical/abc/secret.ics", got)
}

func TestNormalize_WebcalRewritten(t *testing.T) {
	resolver := fakeResolver{addrs: map[string][]net.IP{"p01-calendar.icloud.com": {net.ParseIP("17.0.0.1")}}}
	got, err := Normalize("webcal://p01-calendar.icloud.com/foo.ics", resolver)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(got, "https://"))
}

func TestNormalize_RejectsLoopback(t *testing.T) {
	resolver := fakeResolver{addrs: map[string][]net.IP{"localhost": {net.ParseIP("127.0.0.1")}}}
	_, err := Normalize("http://localhost/feed.ics", resolver)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidURL)
}

func TestNormalize_RejectsPrivate(t *testing.T) {
	resolver := fakeResolver{addrs: map[string][]net.IP{"intra.example": {net.ParseIP("10.0.0.5")}}}
	_, err := Normalize("https://intra.example/feed.ics", resolver)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidURL)
}

func TestNormalize_RejectsFileScheme(t *testing.T) {
	_, err := Normalize("file:///etc/passwd", fakeResolver{})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidURL)
}

func TestNormalize_RejectsEmpty(t *testing.T) {
	_, err := Normalize("   ", fakeResolver{})
	require.Error(t, err)
}
