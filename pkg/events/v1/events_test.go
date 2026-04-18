package eventsv1

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnvelope_WrapsPayloadWithMetadata(t *testing.T) {
	e := UserAuthenticated{
		TelegramID:      42,
		JiraAccountID:   "acc-1",
		CloudID:         "cloud-1",
		SiteURL:         "https://example.atlassian.net",
		Language:        "en",
		AuthenticatedAt: 1_700_000_000,
	}

	raw, err := Marshal(e, "trace-xyz")
	require.NoError(t, err)

	var env Envelope
	require.NoError(t, json.Unmarshal(raw, &env))

	require.Equal(t, SubjectUserAuthenticated, env.Subject)
	require.Equal(t, e.IdempotencyKey(), env.ID)
	require.Equal(t, 1, env.SchemaVersion)
	require.Equal(t, "trace-xyz", env.TraceID)
	require.NotZero(t, env.PublishedAt)

	var back UserAuthenticated
	require.NoError(t, json.Unmarshal(env.Payload, &back))
	require.Equal(t, e, back)
}

func TestIdempotencyKeys_AreStableAndDistinct(t *testing.T) {
	cases := []struct {
		name string
		a, b Event
		same bool
	}{
		{
			name: "user_authenticated same telegramID + ts => same",
			a:    UserAuthenticated{TelegramID: 1, AuthenticatedAt: 100},
			b:    UserAuthenticated{TelegramID: 1, AuthenticatedAt: 100},
			same: true,
		},
		{
			name: "user_authenticated different ts => distinct",
			a:    UserAuthenticated{TelegramID: 1, AuthenticatedAt: 100},
			b:    UserAuthenticated{TelegramID: 1, AuthenticatedAt: 101},
			same: false,
		},
		{
			name: "change_detected same tuple => same",
			a: ChangeDetected{
				SubscriptionID: "s1", IssueKey: "JIRA-1",
				ChangeType: "updated", DetectedAt: 500,
			},
			b: ChangeDetected{
				SubscriptionID: "s1", IssueKey: "JIRA-1",
				ChangeType: "updated", DetectedAt: 500,
			},
			same: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.same {
				require.Equal(t, tc.a.IdempotencyKey(), tc.b.IdempotencyKey())
			} else {
				require.NotEqual(t, tc.a.IdempotencyKey(), tc.b.IdempotencyKey())
			}
		})
	}
}
