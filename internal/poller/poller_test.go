package poller

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"SleepJiraBot/internal/jira"
)

func TestChangeDisplayValue_PrefersStringForm(t *testing.T) {
	assert.Equal(t, "In Progress", changeDisplayValue("In Progress", "3"))
}

func TestChangeDisplayValue_FallsBackToRawWhenEmpty(t *testing.T) {
	// Custom fields sometimes leave the *String form empty and only
	// ship the raw ID; the fallback ensures we still show something.
	assert.Equal(t, "acc-1234", changeDisplayValue("", "acc-1234"))
}

func TestChangeDisplayValue_BothEmpty(t *testing.T) {
	assert.Equal(t, "", changeDisplayValue("", ""))
}

func TestChangeFieldName_PrefersFriendlyName(t *testing.T) {
	assert.Equal(t, "Story Points", changeFieldName("Story Points", "customfield_10016"))
}

func TestChangeFieldName_FallsBackToID(t *testing.T) {
	assert.Equal(t, "customfield_10016", changeFieldName("", "customfield_10016"))
}

func TestChangeFieldName_BothEmpty(t *testing.T) {
	assert.Equal(t, "", changeFieldName("", ""))
}

func TestStripOrderBy_RemovesTopLevelClause(t *testing.T) {
	got := stripOrderBy("project = FOO ORDER BY created DESC")
	assert.Equal(t, "project = FOO", got)
}

func TestStripOrderBy_CaseInsensitive(t *testing.T) {
	got := stripOrderBy("project = FOO order by created DESC")
	assert.Equal(t, "project = FOO", got)
}

func TestStripOrderBy_NoClausePassthrough(t *testing.T) {
	got := stripOrderBy("project = FOO")
	assert.Equal(t, "project = FOO", got)
}

func TestStripOrderBy_LeadingOrderByReturnsEmpty(t *testing.T) {
	// A query that consists solely of an ORDER BY clause has no
	// filterable predicate — callers must get an empty string so they
	// can skip composing with it.
	got := stripOrderBy("ORDER BY created DESC")
	assert.Equal(t, "", got)
}

func TestStripOrderBy_UsesLastOccurrence(t *testing.T) {
	// The field name contains ORDER BY as a substring but the real
	// clause is the last one — LastIndex handles this.
	got := stripOrderBy(`"Approval ORDER BY status" = OK ORDER BY rank`)
	assert.Equal(t, `"Approval ORDER BY status" = OK`, got)
}

func TestStripOrderBy_TrimsWhitespace(t *testing.T) {
	got := stripOrderBy("   project = FOO   ")
	assert.Equal(t, "project = FOO", got)
}

func TestCommentActivityTime_UsesUpdatedWhenNewer(t *testing.T) {
	c := &jira.Comment{
		Created: "2026-04-18T10:00:00.000+0000",
		Updated: "2026-04-18T12:00:00.000+0000",
	}
	got := commentActivityTime(c)
	expected, _ := time.Parse("2006-01-02T15:04:05.000-0700", "2026-04-18T12:00:00.000+0000")
	assert.Equal(t, expected, got)
}

func TestCommentActivityTime_UsesCreatedWhenUpdatedEmpty(t *testing.T) {
	c := &jira.Comment{Created: "2026-04-18T10:00:00.000+0000"}
	got := commentActivityTime(c)
	expected, _ := time.Parse("2006-01-02T15:04:05.000-0700", "2026-04-18T10:00:00.000+0000")
	assert.Equal(t, expected, got)
}

func TestCommentActivityTime_UsesCreatedWhenUpdatedOlder(t *testing.T) {
	// Pathological case: Updated is earlier than Created. We still take
	// the newer of the two so edits can't hide an originally-late event.
	c := &jira.Comment{
		Created: "2026-04-18T10:00:00.000+0000",
		Updated: "2026-04-17T10:00:00.000+0000",
	}
	got := commentActivityTime(c)
	expected, _ := time.Parse("2006-01-02T15:04:05.000-0700", "2026-04-18T10:00:00.000+0000")
	assert.Equal(t, expected, got)
}

func TestCommentActivityTime_UnparseableDates(t *testing.T) {
	c := &jira.Comment{Created: "garbage"}
	got := commentActivityTime(c)
	assert.True(t, got.IsZero())
}

func TestRecentChanges_FiltersBeforeCutoff(t *testing.T) {
	cutoff := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC).Unix()

	issue := &jira.Issue{
		Changelog: &jira.Changelog{
			Histories: []jira.ChangeHistory{
				{
					Created: "2026-04-18T11:00:00.000+0000", // before cutoff
					Author:  &jira.JiraUser{AccountID: "old", DisplayName: "Old"},
					Items:   []jira.ChangeItem{{Field: "status"}},
				},
				{
					Created: "2026-04-18T13:00:00.000+0000", // after cutoff
					Author:  &jira.JiraUser{AccountID: "new", DisplayName: "New"},
					Items:   []jira.ChangeItem{{Field: "assignee"}},
				},
			},
		},
	}

	authors, items := recentChanges(issue, cutoff, "")
	if assert.Len(t, authors, 1) {
		assert.Equal(t, "new", authors[0].AccountID)
	}
	if assert.Len(t, items, 1) {
		assert.Equal(t, "assignee", items[0].Field)
	}
}

func TestRecentChanges_DedupesAuthors(t *testing.T) {
	cutoff := time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC).Unix()
	same := &jira.JiraUser{AccountID: "acc-1", DisplayName: "Alice"}

	issue := &jira.Issue{
		Changelog: &jira.Changelog{
			Histories: []jira.ChangeHistory{
				{
					Created: "2026-04-18T10:00:00.000+0000",
					Author:  same,
					Items:   []jira.ChangeItem{{Field: "status"}},
				},
				{
					Created: "2026-04-18T11:00:00.000+0000",
					Author:  same,
					Items:   []jira.ChangeItem{{Field: "assignee"}},
				},
			},
		},
	}

	authors, items := recentChanges(issue, cutoff, "")
	assert.Len(t, authors, 1, "same account must be listed once")
	assert.Len(t, items, 2, "items from both histories must be returned")
}

func TestRecentChanges_ExcludesAuthorByAccountID(t *testing.T) {
	cutoff := time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC).Unix()

	issue := &jira.Issue{
		Changelog: &jira.Changelog{
			Histories: []jira.ChangeHistory{
				{
					Created: "2026-04-18T10:00:00.000+0000",
					Author:  &jira.JiraUser{AccountID: "self"},
					Items:   []jira.ChangeItem{{Field: "status"}},
				},
				{
					Created: "2026-04-18T11:00:00.000+0000",
					Author:  &jira.JiraUser{AccountID: "other"},
					Items:   []jira.ChangeItem{{Field: "assignee"}},
				},
			},
		},
	}

	authors, items := recentChanges(issue, cutoff, "self")
	if assert.Len(t, authors, 1) {
		assert.Equal(t, "other", authors[0].AccountID)
	}
	assert.Len(t, items, 1)
}

func TestRecentChanges_SkipsUnparseableDate(t *testing.T) {
	cutoff := time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC).Unix()

	issue := &jira.Issue{
		Changelog: &jira.Changelog{
			Histories: []jira.ChangeHistory{
				{
					Created: "garbage",
					Author:  &jira.JiraUser{AccountID: "x"},
					Items:   []jira.ChangeItem{{Field: "status"}},
				},
			},
		},
	}

	authors, items := recentChanges(issue, cutoff, "")
	assert.Empty(t, authors)
	assert.Empty(t, items)
}

func TestRecentChanges_NilChangelog(t *testing.T) {
	authors, items := recentChanges(&jira.Issue{}, 0, "")
	assert.Nil(t, authors)
	assert.Nil(t, items)
}
