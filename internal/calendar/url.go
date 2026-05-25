package calendar

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ErrInvalidURL is returned by Normalize for an unsupported scheme,
// unparseable input, or a host that resolves to a private/loopback
// address (SSRF guard).
var ErrInvalidURL = errors.New("calendar: invalid URL")

// Resolver is the seam Normalize uses to look up the host's IPs.
// Tests inject a fake; production uses net.DefaultResolver.LookupIP.
type Resolver interface {
	LookupIP(host string) ([]net.IP, error)
}

type defaultResolver struct{}

func (defaultResolver) LookupIP(host string) ([]net.IP, error) {
	return net.LookupIP(host)
}

// DefaultResolver is used when none is provided.
var DefaultResolver Resolver = defaultResolver{}

// Normalize trims, validates the URL, rewrites webcal:// → https://, and
// rejects hosts that resolve to private or loopback addresses. The bot
// fetches ICS payloads on behalf of a user; without the SSRF guard a
// malicious user could probe internal services via the bot.
func Normalize(raw string, resolver Resolver) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("%w: empty", ErrInvalidURL)
	}
	if resolver == nil {
		resolver = DefaultResolver
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}

	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "http", "https":
		u.Scheme = scheme
	case "webcal":
		u.Scheme = "https"
	default:
		return "", fmt.Errorf("%w: scheme %q not allowed", ErrInvalidURL, u.Scheme)
	}

	if u.Host == "" {
		return "", fmt.Errorf("%w: missing host", ErrInvalidURL)
	}

	host := u.Hostname()
	ips, err := resolver.LookupIP(host)
	if err != nil {
		return "", fmt.Errorf("%w: cannot resolve %s: %v", ErrInvalidURL, host, err)
	}
	if len(ips) == 0 {
		return "", fmt.Errorf("%w: host %s has no addresses", ErrInvalidURL, host)
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return "", fmt.Errorf("%w: host %s resolves to non-public address %s", ErrInvalidURL, host, ip)
		}
	}

	return u.String(), nil
}
