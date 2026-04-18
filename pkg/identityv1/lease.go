// Package identityv1 defines the v1 wire protocol for the Phase-2
// identity-svc TokenLease endpoint.
//
// Contract: consumers POST a TokenLeaseRequest to /internal/lease and
// receive a short-lived access token plus the Jira cloud/site the user
// is connected to. On HTTP 401 from Jira during refresh, identity-svc
// returns ErrCodeInvalidRefreshToken and the caller should prompt the
// user to reconnect. Transient failures return 5xx and the client
// should back off.
package identityv1

const (
	// LeasePath is the HTTP route where the server listens.
	LeasePath = "/internal/lease"

	// AuthHeader carries the shared-secret bearer token between
	// services. The value is checked literally; treat it as a
	// password rotated out-of-band.
	AuthHeader = "Authorization"
	AuthScheme = "Bearer "
)

// Error codes returned in ErrorResponse.Code.
const (
	ErrCodeNotConnected        = "not_connected"
	ErrCodeInvalidRefreshToken = "invalid_refresh_token"
	ErrCodeRefreshFailed       = "refresh_failed"
	ErrCodeInvalidRequest      = "invalid_request"
	ErrCodeUnauthorized        = "unauthorized"
	ErrCodeInternal            = "internal"
)

// TokenLeaseRequest is the POST body. MinTTLSeconds hints how fresh the
// returned token must be — the server may still return a token whose
// ExpiresAt leaves less margin (the client decides whether to use it).
type TokenLeaseRequest struct {
	TelegramID    int64 `json:"telegram_id"`
	MinTTLSeconds int   `json:"min_ttl_seconds,omitempty"`
}

// TokenLeaseResponse carries enough information to issue one Jira
// request: the bearer token, the cloud id (used to route api.atlassian
// requests), the site URL (for user-facing links and deep-links), and
// the expiry so the client can cache. JiraAccountID is returned so
// consumers can correlate without a second lookup.
type TokenLeaseResponse struct {
	AccessToken   string `json:"access_token"`
	ExpiresAt     int64  `json:"expires_at"`
	CloudID       string `json:"cloud_id"`
	SiteURL       string `json:"site_url"`
	JiraAccountID string `json:"jira_account_id,omitempty"`
}

// ErrorResponse is returned with non-2xx status codes. Code is one of
// the ErrCode* constants above; Message is free-form for logging.
type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}
