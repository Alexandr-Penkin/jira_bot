package telegram

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDiagnoseMissingScopes(t *testing.T) {
	required := []string{"read:jql:jira", "read:issue:jira", "offline_access"}

	t.Run("all granted", func(t *testing.T) {
		missing := diagnoseMissingScopes("read:jql:jira read:issue:jira offline_access", required)
		assert.Empty(t, missing)
	})

	t.Run("partial grant", func(t *testing.T) {
		missing := diagnoseMissingScopes("read:issue:jira offline_access", required)
		assert.Equal(t, []string{"read:jql:jira"}, missing)
	})

	t.Run("preserves required order", func(t *testing.T) {
		missing := diagnoseMissingScopes("", required)
		assert.Equal(t, required, missing)
	})

	t.Run("ignores extra granted scopes", func(t *testing.T) {
		missing := diagnoseMissingScopes("read:jql:jira read:issue:jira offline_access extra:scope", required)
		assert.Empty(t, missing)
	})

	t.Run("handles whitespace variations", func(t *testing.T) {
		missing := diagnoseMissingScopes("  read:jql:jira\tread:issue:jira  offline_access ", required)
		assert.Empty(t, missing)
	})
}

func TestDiagnoseHybridMarkers(t *testing.T) {
	t.Run("empty granted", func(t *testing.T) {
		assert.Nil(t, diagnoseHybridMarkers(""))
	})

	t.Run("pure granular", func(t *testing.T) {
		assert.Nil(t, diagnoseHybridMarkers("read:jql:jira read:issue:jira offline_access"))
	})

	t.Run("classic read mixed in", func(t *testing.T) {
		got := diagnoseHybridMarkers("read:jql:jira read:jira-work offline_access")
		assert.Equal(t, []string{"read:jira-work"}, got)
	})

	t.Run("multiple classics", func(t *testing.T) {
		got := diagnoseHybridMarkers("read:jira-work write:jira-work read:jira-user read:jql:jira")
		assert.Equal(t, []string{"read:jira-work", "write:jira-work", "read:jira-user"}, got)
	})
}
