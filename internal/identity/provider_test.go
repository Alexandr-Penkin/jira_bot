package identity_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"SleepJiraBot/internal/identity"
	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/storage"
	eventsv1 "SleepJiraBot/pkg/events/v1"
	identityv1 "SleepJiraBot/pkg/identityv1"
)

type fakeUserStore struct {
	mu        sync.Mutex
	user      *storage.User
	updateErr error
	updates   []updateCall
}

type updateCall struct {
	ID        int64
	Access    string
	Refresh   string
	ExpiresAt time.Time
}

func (f *fakeUserStore) GetByTelegramID(_ context.Context, id int64) (*storage.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.user == nil || f.user.TelegramUserID != id {
		return nil, nil
	}
	copy := *f.user
	return &copy, nil
}

func (f *fakeUserStore) UpdateTokens(_ context.Context, id int64, access, refresh string, expiresAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updateErr != nil {
		return f.updateErr
	}
	f.updates = append(f.updates, updateCall{id, access, refresh, expiresAt})
	if f.user != nil && f.user.TelegramUserID == id {
		f.user.AccessToken = access
		f.user.RefreshToken = refresh
		f.user.TokenExpiresAt = expiresAt
	}
	return nil
}

type fakeRefresher struct {
	calls int
	resp  *jira.TokenResponse
	err   error
}

func (f *fakeRefresher) RefreshTokens(_ context.Context, _ string) (*jira.TokenResponse, error) {
	f.calls++
	return f.resp, f.err
}

func (f *fakeRefresher) TokenExpiresAt(expiresIn int) time.Time {
	return time.Now().Add(time.Duration(expiresIn) * time.Second)
}

type capturingPublisher struct {
	mu     sync.Mutex
	events []any
}

func (c *capturingPublisher) Publish(_ context.Context, ev eventsv1.Event, _ string) error {
	c.mu.Lock()
	c.events = append(c.events, ev)
	c.mu.Unlock()
	return nil
}

func (c *capturingPublisher) Close() error { return nil }

func (c *capturingPublisher) snapshot() []any {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]any, len(c.events))
	copy(out, c.events)
	return out
}

func TestLocalProvider_Lease_RejectsZeroTelegramID(t *testing.T) {
	p := identity.NewLocalProvider(&fakeUserStore{}, &fakeRefresher{}, zerolog.Nop())

	_, err := p.Lease(context.Background(), identityv1.TokenLeaseRequest{})
	require.Error(t, err)

	var le *identity.LeaseError
	require.True(t, errors.As(err, &le))
	assert.Equal(t, identityv1.ErrCodeInvalidRequest, le.Code)
}

func TestLocalProvider_Lease_NotConnectedWhenUserMissing(t *testing.T) {
	p := identity.NewLocalProvider(&fakeUserStore{}, &fakeRefresher{}, zerolog.Nop())

	_, err := p.Lease(context.Background(), identityv1.TokenLeaseRequest{TelegramID: 7})
	require.Error(t, err)

	var le *identity.LeaseError
	require.True(t, errors.As(err, &le))
	assert.Equal(t, identityv1.ErrCodeNotConnected, le.Code)
}

func TestLocalProvider_Lease_NotConnectedWhenTokenEmpty(t *testing.T) {
	store := &fakeUserStore{user: &storage.User{TelegramUserID: 7}}
	p := identity.NewLocalProvider(store, &fakeRefresher{}, zerolog.Nop())

	_, err := p.Lease(context.Background(), identityv1.TokenLeaseRequest{TelegramID: 7})
	require.Error(t, err)

	var le *identity.LeaseError
	require.True(t, errors.As(err, &le))
	assert.Equal(t, identityv1.ErrCodeNotConnected, le.Code)
}

func TestLocalProvider_Lease_ReturnsCachedWhenFresh(t *testing.T) {
	store := &fakeUserStore{user: &storage.User{
		TelegramUserID: 7,
		AccessToken:    "fresh",
		RefreshToken:   "r",
		TokenExpiresAt: time.Now().Add(30 * time.Minute),
		JiraCloudID:    "cloud",
		JiraSiteURL:    "https://x.atlassian.net",
		JiraAccountID:  "acc",
	}}
	ref := &fakeRefresher{}
	p := identity.NewLocalProvider(store, ref, zerolog.Nop())

	resp, err := p.Lease(context.Background(), identityv1.TokenLeaseRequest{TelegramID: 7})
	require.NoError(t, err)
	assert.Equal(t, "fresh", resp.AccessToken)
	assert.Equal(t, "cloud", resp.CloudID)
	assert.Equal(t, "https://x.atlassian.net", resp.SiteURL)
	assert.Equal(t, "acc", resp.JiraAccountID)
	assert.Equal(t, 0, ref.calls, "no refresh should fire")
}

func TestLocalProvider_Lease_RefreshesWhenExpired(t *testing.T) {
	store := &fakeUserStore{user: &storage.User{
		TelegramUserID: 7,
		AccessToken:    "stale",
		RefreshToken:   "rtoken",
		TokenExpiresAt: time.Now().Add(-time.Minute),
	}}
	ref := &fakeRefresher{
		resp: &jira.TokenResponse{AccessToken: "newaccess", RefreshToken: "newrefresh", ExpiresIn: 3600},
	}
	pub := &capturingPublisher{}
	p := identity.NewLocalProvider(store, ref, zerolog.Nop())
	p.SetEventPublisher(pub)

	resp, err := p.Lease(context.Background(), identityv1.TokenLeaseRequest{TelegramID: 7})
	require.NoError(t, err)
	assert.Equal(t, "newaccess", resp.AccessToken)
	assert.Equal(t, 1, ref.calls)
	require.Len(t, store.updates, 1)
	assert.Equal(t, "newaccess", store.updates[0].Access)
	assert.Equal(t, "newrefresh", store.updates[0].Refresh)

	// TokensRefreshed event emitted.
	events := pub.snapshot()
	require.Len(t, events, 1)
	_, ok := events[0].(eventsv1.TokensRefreshed)
	assert.True(t, ok)
}

func TestLocalProvider_Lease_KeepsOldRefreshWhenRotationOmitted(t *testing.T) {
	store := &fakeUserStore{user: &storage.User{
		TelegramUserID: 7,
		AccessToken:    "stale",
		RefreshToken:   "oldrefresh",
		TokenExpiresAt: time.Now().Add(-time.Minute),
	}}
	ref := &fakeRefresher{
		resp: &jira.TokenResponse{AccessToken: "newaccess", RefreshToken: "", ExpiresIn: 3600},
	}
	p := identity.NewLocalProvider(store, ref, zerolog.Nop())

	_, err := p.Lease(context.Background(), identityv1.TokenLeaseRequest{TelegramID: 7})
	require.NoError(t, err)

	require.Len(t, store.updates, 1)
	assert.Equal(t, "oldrefresh", store.updates[0].Refresh, "must preserve prior refresh token")
}

func TestLocalProvider_Lease_InvalidRefreshBubblesAsLeaseError(t *testing.T) {
	store := &fakeUserStore{user: &storage.User{
		TelegramUserID: 7,
		AccessToken:    "stale",
		RefreshToken:   "rtoken",
		TokenExpiresAt: time.Now().Add(-time.Minute),
	}}
	ref := &fakeRefresher{err: jira.ErrTokenInvalid}
	p := identity.NewLocalProvider(store, ref, zerolog.Nop())

	_, err := p.Lease(context.Background(), identityv1.TokenLeaseRequest{TelegramID: 7})
	require.Error(t, err)

	var le *identity.LeaseError
	require.True(t, errors.As(err, &le))
	assert.Equal(t, identityv1.ErrCodeInvalidRefreshToken, le.Code)
}

func TestLocalProvider_Lease_OtherRefreshErrorMapsToRefreshFailed(t *testing.T) {
	store := &fakeUserStore{user: &storage.User{
		TelegramUserID: 7,
		AccessToken:    "stale",
		RefreshToken:   "rtoken",
		TokenExpiresAt: time.Now().Add(-time.Minute),
	}}
	ref := &fakeRefresher{err: errors.New("boom")}
	p := identity.NewLocalProvider(store, ref, zerolog.Nop())

	_, err := p.Lease(context.Background(), identityv1.TokenLeaseRequest{TelegramID: 7})
	require.Error(t, err)

	var le *identity.LeaseError
	require.True(t, errors.As(err, &le))
	assert.Equal(t, identityv1.ErrCodeRefreshFailed, le.Code)
}

func TestLocalProvider_Lease_MinTTLBeyondSkewTriggersRefresh(t *testing.T) {
	// Token has 2 minutes left — fresh for default skew, but caller asks
	// for 10 minutes minimum.
	store := &fakeUserStore{user: &storage.User{
		TelegramUserID: 7,
		AccessToken:    "access-short",
		RefreshToken:   "r",
		TokenExpiresAt: time.Now().Add(2 * time.Minute),
	}}
	ref := &fakeRefresher{
		resp: &jira.TokenResponse{AccessToken: "access-long", RefreshToken: "r2", ExpiresIn: 3600},
	}
	p := identity.NewLocalProvider(store, ref, zerolog.Nop())

	resp, err := p.Lease(context.Background(), identityv1.TokenLeaseRequest{
		TelegramID:    7,
		MinTTLSeconds: int((10 * time.Minute).Seconds()),
	})
	require.NoError(t, err)
	assert.Equal(t, "access-long", resp.AccessToken)
	assert.Equal(t, 1, ref.calls)
}

func TestLocalProvider_Lease_ConcurrentRefreshSerialised(t *testing.T) {
	// Two concurrent Lease calls for the same user must result in exactly
	// one RefreshTokens call thanks to the per-user mutex + re-read-after-
	// lock pattern.
	store := &fakeUserStore{user: &storage.User{
		TelegramUserID: 7,
		AccessToken:    "stale",
		RefreshToken:   "r",
		TokenExpiresAt: time.Now().Add(-time.Minute),
	}}
	ref := &fakeRefresher{
		resp: &jira.TokenResponse{AccessToken: "new", RefreshToken: "r2", ExpiresIn: 3600},
	}
	p := identity.NewLocalProvider(store, ref, zerolog.Nop())

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := p.Lease(context.Background(), identityv1.TokenLeaseRequest{TelegramID: 7})
			assert.NoError(t, err)
		}()
	}
	wg.Wait()

	assert.Equal(t, 1, ref.calls, "only the first goroutine inside the lock should refresh")
}
