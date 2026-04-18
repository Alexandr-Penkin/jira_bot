// Package identity owns the Phase-2 TokenLease protocol. LocalProvider
// replicates the refresh flow previously embedded in jira.Client; Server
// exposes it as an HTTP endpoint so the monolith and cmd/identity-svc
// share one implementation. In Phase 2b jira.Client will be refactored
// to consume a TokenProvider (either LocalProvider in-process or a
// remote HTTP client), at which point the duplication between the
// provider and jira.Client.ensureValidToken goes away.
package identity

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/storage"
	eventsv1 "SleepJiraBot/pkg/events/v1"
	identityv1 "SleepJiraBot/pkg/identityv1"
)

// refreshSkew is how long before TokenExpiresAt the provider treats a
// token as stale. Must mirror jira.Client.ensureValidToken's constant to
// avoid both paths fighting over the same token.
const refreshSkew = 60 * time.Second

// Provider is the minimal surface callers need; LocalProvider is the
// only impl for now. A remote HTTP impl lives in pkg/identityclient.
type Provider interface {
	Lease(ctx context.Context, req identityv1.TokenLeaseRequest) (*identityv1.TokenLeaseResponse, error)
}

// UserStore is the minimal UserRepo surface LocalProvider touches. Kept
// as an interface so tests can inject a fake without Mongo.
type UserStore interface {
	GetByTelegramID(ctx context.Context, telegramUserID int64) (*storage.User, error)
	UpdateTokens(ctx context.Context, telegramUserID int64, accessToken, refreshToken string, expiresAt time.Time) error
}

// TokenRefresher is the subset of jira.OAuthClient LocalProvider needs.
type TokenRefresher interface {
	RefreshTokens(ctx context.Context, refreshToken string) (*jira.TokenResponse, error)
	TokenExpiresAt(expiresIn int) time.Time
}

// LocalProvider wraps UserStore + TokenRefresher to serve lease requests
// in the same process. It is safe for concurrent use.
type LocalProvider struct {
	userRepo UserStore
	oauth    TokenRefresher
	log      zerolog.Logger
	pub      eventsv1.Publisher

	locksMu    sync.Mutex
	tokenLocks map[int64]*sync.Mutex
}

func NewLocalProvider(userRepo UserStore, oauth TokenRefresher, log zerolog.Logger) *LocalProvider {
	return &LocalProvider{
		userRepo:   userRepo,
		oauth:      oauth,
		log:        log,
		pub:        eventsv1.NoopPublisher{},
		tokenLocks: make(map[int64]*sync.Mutex),
	}
}

// SetEventPublisher installs a domain event publisher. TokensRefreshed
// fires when the provider completes a successful refresh.
func (p *LocalProvider) SetEventPublisher(pub eventsv1.Publisher) {
	if pub == nil {
		p.pub = eventsv1.NoopPublisher{}
		return
	}
	p.pub = pub
}

func (p *LocalProvider) lock(telegramID int64) *sync.Mutex {
	p.locksMu.Lock()
	defer p.locksMu.Unlock()
	mu, ok := p.tokenLocks[telegramID]
	if !ok {
		mu = &sync.Mutex{}
		p.tokenLocks[telegramID] = mu
	}
	return mu
}

// Lease returns a current access token for the given Telegram user,
// refreshing on demand. The response includes everything a downstream
// caller needs to issue a Jira request (cloud id, site url).
func (p *LocalProvider) Lease(ctx context.Context, req identityv1.TokenLeaseRequest) (*identityv1.TokenLeaseResponse, error) {
	if req.TelegramID == 0 {
		return nil, &LeaseError{Code: identityv1.ErrCodeInvalidRequest, Message: "telegram_id is required"}
	}

	user, err := p.userRepo.GetByTelegramID(ctx, req.TelegramID)
	if err != nil {
		return nil, fmt.Errorf("read user: %w", err)
	}
	if user == nil || user.AccessToken == "" {
		return nil, &LeaseError{Code: identityv1.ErrCodeNotConnected, Message: "user has not connected Jira"}
	}

	minTTL := time.Duration(req.MinTTLSeconds) * time.Second
	if minTTL < refreshSkew {
		minTTL = refreshSkew
	}

	if time.Now().Add(minTTL).Before(user.TokenExpiresAt) {
		return buildResponse(user), nil
	}

	mu := p.lock(req.TelegramID)
	mu.Lock()
	defer mu.Unlock()

	fresh, err := p.userRepo.GetByTelegramID(ctx, req.TelegramID)
	if err != nil {
		return nil, fmt.Errorf("re-read user: %w", err)
	}
	if fresh == nil || fresh.AccessToken == "" {
		return nil, &LeaseError{Code: identityv1.ErrCodeNotConnected, Message: "user disconnected while refreshing"}
	}
	if time.Now().Add(minTTL).Before(fresh.TokenExpiresAt) {
		return buildResponse(fresh), nil
	}

	p.log.Debug().Int64("telegram_user_id", req.TelegramID).Msg("identity: refreshing jira token")

	tokenResp, err := p.oauth.RefreshTokens(ctx, fresh.RefreshToken)
	if err != nil {
		if errors.Is(err, jira.ErrTokenInvalid) {
			return nil, &LeaseError{Code: identityv1.ErrCodeInvalidRefreshToken, Message: err.Error()}
		}
		return nil, &LeaseError{Code: identityv1.ErrCodeRefreshFailed, Message: err.Error()}
	}

	newAccess := tokenResp.AccessToken
	newRefresh := fresh.RefreshToken
	if tokenResp.RefreshToken != "" {
		newRefresh = tokenResp.RefreshToken
	}
	newExpiresAt := p.oauth.TokenExpiresAt(tokenResp.ExpiresIn)

	if err := p.userRepo.UpdateTokens(ctx, req.TelegramID, newAccess, newRefresh, newExpiresAt); err != nil {
		return nil, fmt.Errorf("save refreshed tokens: %w", err)
	}

	if pubErr := p.pub.Publish(ctx, eventsv1.TokensRefreshed{
		TelegramID:  req.TelegramID,
		RefreshedAt: time.Now().Unix(),
		ExpiresAt:   newExpiresAt.Unix(),
	}, ""); pubErr != nil {
		p.log.Warn().Err(pubErr).Int64("telegram_user_id", req.TelegramID).Msg("identity: publish tokens_refreshed failed")
	}

	return &identityv1.TokenLeaseResponse{
		AccessToken:   newAccess,
		ExpiresAt:     newExpiresAt.Unix(),
		CloudID:       fresh.JiraCloudID,
		SiteURL:       fresh.JiraSiteURL,
		JiraAccountID: fresh.JiraAccountID,
	}, nil
}

func buildResponse(user *storage.User) *identityv1.TokenLeaseResponse {
	return &identityv1.TokenLeaseResponse{
		AccessToken:   user.AccessToken,
		ExpiresAt:     user.TokenExpiresAt.Unix(),
		CloudID:       user.JiraCloudID,
		SiteURL:       user.JiraSiteURL,
		JiraAccountID: user.JiraAccountID,
	}
}

// LeaseError carries a protocol-level error code that Server maps to an
// HTTP status. Non-protocol errors (e.g. Mongo failures) are returned as
// plain errors and become 500.
type LeaseError struct {
	Code    string
	Message string
}

func (e *LeaseError) Error() string { return e.Code + ": " + e.Message }
