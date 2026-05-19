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
