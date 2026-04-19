package eventsv1

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestContract_GoldenPayloads pins the on-wire JSON shape of every v1
// event payload. A diff here means the contract changed:
//   - renaming or removing a field is a breaking change — bump to vN+1
//     and dual-publish for one release.
//   - adding an optional field is backward-compatible — update the
//     corresponding golden in this table *and* add an unmarshal-round-trip
//     assertion in TestContract_AdditiveCompatibility below.
//
// JSONEq is used so field declaration order can be rearranged within a
// struct without breaking the contract — what matters is the key set +
// value set, not byte-exact order.
func TestContract_GoldenPayloads(t *testing.T) {
	cases := []struct {
		name   string
		event  any
		golden string
	}{
		// --- identity ---
		{
			name: "UserAuthenticated",
			event: UserAuthenticated{
				TelegramID:      42,
				JiraAccountID:   "acc-1",
				CloudID:         "cloud-1",
				SiteURL:         "https://example.atlassian.net",
				Language:        "en",
				AuthenticatedAt: 1_700_000_000,
			},
			golden: `{
				"telegram_id": 42,
				"jira_account_id": "acc-1",
				"cloud_id": "cloud-1",
				"site_url": "https://example.atlassian.net",
				"language": "en",
				"authenticated_at": 1700000000
			}`,
		},
		{
			name: "TokensRefreshed",
			event: TokensRefreshed{
				TelegramID:  42,
				RefreshedAt: 1_700_000_000,
				ExpiresAt:   1_700_003_600,
			},
			golden: `{
				"telegram_id": 42,
				"refreshed_at": 1700000000,
				"expires_at": 1700003600
			}`,
		},
		{
			name: "UserDeauthorized",
			event: UserDeauthorized{
				TelegramID: 42,
				Reason:     "invalid_grant",
				At:         1_700_000_000,
			},
			golden: `{
				"telegram_id": 42,
				"reason": "invalid_grant",
				"at": 1700000000
			}`,
		},
		// --- preferences ---
		{
			name: "LanguageChanged",
			event: LanguageChanged{
				TelegramID: 42,
				Language:   "ru",
				At:         1_700_000_000,
			},
			golden: `{
				"telegram_id": 42,
				"language": "ru",
				"at": 1700000000
			}`,
		},
		{
			name: "DefaultsChanged",
			event: DefaultsChanged{
				TelegramID:         42,
				DefaultProject:     "PROJ",
				DefaultBoardID:     7,
				SprintIssueTypes:   []string{"Story", "Task"},
				AssigneeFieldID:    "customfield_10001",
				StoryPointsFieldID: "customfield_10016",
				DoneStatuses:       []string{"Done", "Closed"},
				HoldStatuses:       []string{"Blocked"},
				DailyDoneJQL:       "status = Done",
				DailyDoingJQL:      "status = \"In Progress\"",
				DailyPlanJQL:       "status = \"To Do\"",
				At:                 1_700_000_000,
			},
			golden: `{
				"telegram_id": 42,
				"default_project": "PROJ",
				"default_board_id": 7,
				"sprint_issue_types": ["Story", "Task"],
				"assignee_field_id": "customfield_10001",
				"story_points_field_id": "customfield_10016",
				"done_statuses": ["Done", "Closed"],
				"hold_statuses": ["Blocked"],
				"daily_done_jql": "status = Done",
				"daily_doing_jql": "status = \"In Progress\"",
				"daily_plan_jql": "status = \"To Do\"",
				"at": 1700000000
			}`,
		},
		// --- subscription ---
		{
			name: "SubscriptionCreated",
			event: SubscriptionCreated{
				SubscriptionID:   "sub-1",
				TelegramID:       42,
				ChatID:           -100,
				SubscriptionType: "my_mentions",
				ProjectKey:       "PROJ",
				IssueKey:         "PROJ-1",
				FilterID:         "10001",
				FilterName:       "my-open",
				FilterJQL:        "assignee = currentUser()",
				At:               1_700_000_000,
			},
			golden: `{
				"subscription_id": "sub-1",
				"telegram_id": 42,
				"chat_id": -100,
				"subscription_type": "my_mentions",
				"project_key": "PROJ",
				"issue_key": "PROJ-1",
				"filter_id": "10001",
				"filter_name": "my-open",
				"filter_jql": "assignee = currentUser()",
				"at": 1700000000
			}`,
		},
		{
			name: "SubscriptionDeleted",
			event: SubscriptionDeleted{
				SubscriptionID: "sub-1",
				TelegramID:     42,
				ChatID:         -100,
				At:             1_700_000_000,
			},
			golden: `{
				"subscription_id": "sub-1",
				"telegram_id": 42,
				"chat_id": -100,
				"at": 1700000000
			}`,
		},
		{
			name: "ChangeDetected",
			event: ChangeDetected{
				SubscriptionID:   "sub-1",
				SubscriptionType: "my_mentions",
				TelegramID:       42,
				ChatID:           -100,
				IssueKey:         "PROJ-1",
				ChangeType:       "updated",
				Actor:            "alice",
				DetectedAt:       1_700_000_000,
			},
			golden: `{
				"subscription_id": "sub-1",
				"subscription_type": "my_mentions",
				"telegram_id": 42,
				"chat_id": -100,
				"issue_key": "PROJ-1",
				"change_type": "updated",
				"actor": "alice",
				"detected_at": 1700000000
			}`,
		},
		// --- webhook ---
		{
			name: "WebhookReceived",
			event: WebhookReceived{
				Source:            "jira",
				EventType:         "jira:issue_updated",
				JiraEventID:       "evt-1",
				ReceivedAt:        1_700_000_000,
				SignatureVerified: true,
				Payload:           json.RawMessage(`{"issue":{"key":"PROJ-1"}}`),
			},
			golden: `{
				"source": "jira",
				"event_type": "jira:issue_updated",
				"jira_event_id": "evt-1",
				"received_at": 1700000000,
				"signature_verified": true,
				"payload": {"issue":{"key":"PROJ-1"}}
			}`,
		},
		{
			name: "WebhookNormalized",
			event: WebhookNormalized{
				EventType:   "jira:issue_updated",
				IssueKey:    "PROJ-1",
				ProjectKey:  "PROJ",
				ChangeType:  "updated",
				Actor:       "alice",
				At:          1_700_000_000,
				JiraEventID: "evt-1",
				Affected: []WebhookAffected{
					{TelegramID: 42, ChatID: -100, SubscriptionID: "sub-1", SubscriptionType: "my_mentions"},
				},
				Payload: json.RawMessage(`{"issue":{"key":"PROJ-1"}}`),
			},
			golden: `{
				"event_type": "jira:issue_updated",
				"issue_key": "PROJ-1",
				"project_key": "PROJ",
				"change_type": "updated",
				"actor": "alice",
				"at": 1700000000,
				"jira_event_id": "evt-1",
				"affected": [{
					"telegram_id": 42,
					"chat_id": -100,
					"subscription_id": "sub-1",
					"subscription_type": "my_mentions"
				}],
				"payload": {"issue":{"key":"PROJ-1"}}
			}`,
		},
		// --- schedule ---
		{
			name: "ScheduleDue",
			event: ScheduleDue{
				ReportID:   "r-1",
				TelegramID: 42,
				ChatID:     -100,
				JQL:        "project = PROJ",
				ReportName: "daily",
				FiredAt:    1_700_000_000,
			},
			golden: `{
				"report_id": "r-1",
				"telegram_id": 42,
				"chat_id": -100,
				"jql": "project = PROJ",
				"report_name": "daily",
				"fired_at": 1700000000
			}`,
		},
		// --- notify ---
		{
			name: "NotifyRequested",
			event: NotifyRequested{
				ChatID:                -100,
				TelegramID:            42,
				Text:                  "hello",
				ParseMode:             "MarkdownV2",
				DisableWebPagePreview: true,
				DedupKey:              "poller:chat:-100:issue:PROJ-1",
				Reason:                "poller:assigned_to_me",
				RequestedAt:           1_700_000_000,
			},
			golden: `{
				"chat_id": -100,
				"telegram_id": 42,
				"text": "hello",
				"parse_mode": "MarkdownV2",
				"disable_web_page_preview": true,
				"dedup_key": "poller:chat:-100:issue:PROJ-1",
				"reason": "poller:assigned_to_me",
				"requested_at": 1700000000
			}`,
		},
		{
			name: "NotifyDelivered",
			event: NotifyDelivered{
				ChatID:        -100,
				TelegramID:    42,
				DedupKey:      "poller:chat:-100:issue:PROJ-1",
				TelegramMsgID: 9001,
				DeliveredAt:   1_700_000_000,
			},
			golden: `{
				"chat_id": -100,
				"telegram_id": 42,
				"dedup_key": "poller:chat:-100:issue:PROJ-1",
				"telegram_msg_id": 9001,
				"delivered_at": 1700000000
			}`,
		},
		{
			name: "NotifyFailed",
			event: NotifyFailed{
				ChatID:     -100,
				TelegramID: 42,
				DedupKey:   "poller:chat:-100:issue:PROJ-1",
				Reason:     "forbidden: bot blocked",
				Retryable:  false,
				FailedAt:   1_700_000_000,
			},
			golden: `{
				"chat_id": -100,
				"telegram_id": 42,
				"dedup_key": "poller:chat:-100:issue:PROJ-1",
				"reason": "forbidden: bot blocked",
				"retryable": false,
				"failed_at": 1700000000
			}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.event)
			require.NoError(t, err)
			assert.JSONEq(t, tc.golden, string(raw),
				"on-wire shape changed for %s — either revert, bump to vN+1, or update the golden with full review", tc.name)
		})
	}
}

// TestContract_OmitemptyFields pins which fields drop out when zero.
// Consumers must never rely on a field being present if its producer side
// marks it omitempty — this test documents and enforces the current
// optional set.
func TestContract_OmitemptyFields(t *testing.T) {
	cases := []struct {
		name     string
		event    any
		absent   []string // must NOT appear in the JSON
		required []string // MUST appear even when zero
	}{
		{
			name:     "UserAuthenticated_emptyLanguage",
			event:    UserAuthenticated{TelegramID: 1, AuthenticatedAt: 100},
			absent:   []string{"language"},
			required: []string{"telegram_id", "authenticated_at", "jira_account_id", "cloud_id", "site_url"},
		},
		{
			name:     "NotifyRequested_minimal",
			event:    NotifyRequested{ChatID: -1, Text: "hi", DedupKey: "k", RequestedAt: 1},
			absent:   []string{"telegram_id", "parse_mode", "disable_web_page_preview", "reason"},
			required: []string{"chat_id", "text", "dedup_key", "requested_at"},
		},
		{
			name:     "SubscriptionCreated_minimal",
			event:    SubscriptionCreated{SubscriptionID: "s1", TelegramID: 1, ChatID: -1, SubscriptionType: "t", At: 1},
			absent:   []string{"project_key", "issue_key", "filter_id", "filter_name", "filter_jql"},
			required: []string{"subscription_id", "telegram_id", "chat_id", "subscription_type", "at"},
		},
		{
			name:     "WebhookNormalized_minimal",
			event:    WebhookNormalized{EventType: "x", ChangeType: "c", At: 1, Affected: []WebhookAffected{}},
			absent:   []string{"issue_key", "project_key", "actor", "jira_event_id", "payload"},
			required: []string{"event_type", "change_type", "at", "affected"},
		},
		{
			name:     "DefaultsChanged_minimal",
			event:    DefaultsChanged{TelegramID: 1, At: 1},
			absent:   []string{"default_project", "default_board_id", "sprint_issue_types", "assignee_field_id", "story_points_field_id", "done_statuses", "hold_statuses", "daily_done_jql", "daily_doing_jql", "daily_plan_jql"},
			required: []string{"telegram_id", "at"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.event)
			require.NoError(t, err)

			var generic map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw, &generic))

			for _, k := range tc.required {
				_, ok := generic[k]
				assert.Truef(t, ok, "required key %q missing from %s payload: %s", k, tc.name, raw)
			}
			for _, k := range tc.absent {
				_, ok := generic[k]
				assert.Falsef(t, ok, "omitempty key %q leaked into %s payload: %s", k, tc.name, raw)
			}
		})
	}
}

// TestContract_AdditiveCompatibility asserts that consumers can decode a
// payload with an unknown field — the additive-only evolution rule relies
// on this. Go's default decoder ignores unknowns; this test pins that
// behaviour so nobody sneaks in a DisallowUnknownFields call on the hot
// path.
func TestContract_AdditiveCompatibility(t *testing.T) {
	// Future producer added "nickname" to UserAuthenticated in v1.1.
	// A current consumer must still decode the known fields.
	forward := `{
		"telegram_id": 42,
		"jira_account_id": "acc-1",
		"cloud_id": "cloud-1",
		"site_url": "https://example.atlassian.net",
		"language": "en",
		"authenticated_at": 1700000000,
		"nickname": "future-field"
	}`

	var e UserAuthenticated
	require.NoError(t, json.Unmarshal([]byte(forward), &e))
	assert.Equal(t, int64(42), e.TelegramID)
	assert.Equal(t, "acc-1", e.JiraAccountID)
}

// TestContract_EnvelopeShape pins the Envelope's on-wire key set. Unlike
// payload goldens this does not pin PublishedAt (time.Now-driven), but it
// asserts that every envelope-level field is present and no new ones have
// been silently added.
func TestContract_EnvelopeShape(t *testing.T) {
	raw, err := Marshal(UserAuthenticated{TelegramID: 1, AuthenticatedAt: 100}, "trace-abc")
	require.NoError(t, err)

	var generic map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &generic))

	expectedKeys := []string{"id", "subject", "published_at", "schema_version", "trace_id", "payload"}
	for _, k := range expectedKeys {
		_, ok := generic[k]
		assert.Truef(t, ok, "envelope missing required key %q in %s", k, raw)
	}
	// Fail if unexpected top-level keys appear; forces a review when the
	// envelope is expanded.
	for k := range generic {
		assert.Truef(t, slices.Contains(expectedKeys, k),
			"unexpected envelope key %q — either revert or update the contract test", k)
	}
}

// TestContract_SubjectsArePinned confirms the subject string constants
// match the documented scheme sjb.<context>.<aggregate>.<event>.vN. If a
// stream rename or version bump is intended, update the const *and* this
// test together.
func TestContract_SubjectsArePinned(t *testing.T) {
	pinned := map[string]string{
		"UserAuthenticated":   "sjb.identity.user.authenticated.v1",
		"TokensRefreshed":     "sjb.identity.tokens.refreshed.v1",
		"UserDeauthorized":    "sjb.identity.user.deauthorized.v1",
		"LanguageChanged":     "sjb.preferences.language.changed.v1",
		"DefaultsChanged":     "sjb.preferences.defaults.changed.v1",
		"SubscriptionCreated": "sjb.subscription.created.v1",
		"SubscriptionDeleted": "sjb.subscription.deleted.v1",
		"ChangeDetected":      "sjb.subscription.change_detected.v1",
		"WebhookReceived":     "sjb.webhook.received.v1",
		"WebhookNormalized":   "sjb.webhook.normalized.v1",
		"ScheduleDue":         "sjb.schedule.due.v1",
		"NotifyRequested":     "sjb.notify.requested.v1",
		"NotifyDelivered":     "sjb.notify.delivered.v1",
		"NotifyFailed":        "sjb.notify.failed.v1",
	}

	assert.Equal(t, pinned["UserAuthenticated"], UserAuthenticated{}.Subject())
	assert.Equal(t, pinned["TokensRefreshed"], TokensRefreshed{}.Subject())
	assert.Equal(t, pinned["UserDeauthorized"], UserDeauthorized{}.Subject())
	assert.Equal(t, pinned["LanguageChanged"], LanguageChanged{}.Subject())
	assert.Equal(t, pinned["DefaultsChanged"], (&DefaultsChanged{}).Subject())
	assert.Equal(t, pinned["SubscriptionCreated"], (&SubscriptionCreated{}).Subject())
	assert.Equal(t, pinned["SubscriptionDeleted"], SubscriptionDeleted{}.Subject())
	assert.Equal(t, pinned["ChangeDetected"], ChangeDetected{}.Subject())
	assert.Equal(t, pinned["WebhookReceived"], WebhookReceived{}.Subject())
	assert.Equal(t, pinned["WebhookNormalized"], (&WebhookNormalized{}).Subject())
	assert.Equal(t, pinned["ScheduleDue"], ScheduleDue{}.Subject())
	assert.Equal(t, pinned["NotifyRequested"], NotifyRequested{}.Subject())
	assert.Equal(t, pinned["NotifyDelivered"], NotifyDelivered{}.Subject())
	assert.Equal(t, pinned["NotifyFailed"], NotifyFailed{}.Subject())
}
