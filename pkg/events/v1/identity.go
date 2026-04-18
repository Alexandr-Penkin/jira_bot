package eventsv1

import "strconv"

// UserAuthenticated fires after a successful OAuth 3LO exchange where the
// monolith has persisted tokens and resolved Jira identity.
type UserAuthenticated struct {
	TelegramID      int64  `json:"telegram_id"`
	JiraAccountID   string `json:"jira_account_id"`
	CloudID         string `json:"cloud_id"`
	SiteURL         string `json:"site_url"`
	Language        string `json:"language,omitempty"`
	AuthenticatedAt int64  `json:"authenticated_at"`
}

func (UserAuthenticated) Subject() string { return SubjectUserAuthenticated }

func (e UserAuthenticated) IdempotencyKey() string {
	return "identity.user.authenticated:" + strconv.FormatInt(e.TelegramID, 10) +
		":" + strconv.FormatInt(e.AuthenticatedAt, 10)
}

// TokensRefreshed fires after a successful OAuth refresh_token exchange.
type TokensRefreshed struct {
	TelegramID  int64 `json:"telegram_id"`
	RefreshedAt int64 `json:"refreshed_at"`
	ExpiresAt   int64 `json:"expires_at"`
}

func (TokensRefreshed) Subject() string { return SubjectTokensRefreshed }

func (e TokensRefreshed) IdempotencyKey() string {
	return "identity.tokens.refreshed:" + strconv.FormatInt(e.TelegramID, 10) +
		":" + strconv.FormatInt(e.RefreshedAt, 10)
}

// UserDeauthorized fires when a user's Jira tokens are revoked / expired
// beyond recovery and the monolith drops them.
type UserDeauthorized struct {
	TelegramID int64  `json:"telegram_id"`
	Reason     string `json:"reason"`
	At         int64  `json:"at"`
}

func (UserDeauthorized) Subject() string { return SubjectUserDeauthorized }

func (e UserDeauthorized) IdempotencyKey() string {
	return "identity.user.deauthorized:" + strconv.FormatInt(e.TelegramID, 10) +
		":" + strconv.FormatInt(e.At, 10)
}
