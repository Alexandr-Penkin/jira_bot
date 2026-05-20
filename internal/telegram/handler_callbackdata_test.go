package telegram

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"

	"SleepJiraBot/internal/locale"
)

// Regression: Jira-defined names containing ':' used to land in
// callback-data and get truncated by handler.go's SplitN(":", 4).
// These tests freeze the new index-based contract.

func TestIssueTypeKeyboard_NamesWithColonStayIntact(t *testing.T) {
	stateData := map[string]string{
		"project":       "PROJ",
		"nm_count":      "3",
		"nm:0":          "Task",
		"nm:1":          "API: REST",     // colon
		"nm:2":          "Foo: Bar: Baz", // multiple colons
		"sel:API: REST": "1",
	}

	kb := buildIssueTypeKeyboard(locale.EN, stateData)

	// 3 issue type buttons + 1 save/clear row.
	assert.Len(t, kb.InlineKeyboard, 4)

	for i, row := range kb.InlineKeyboard[:3] {
		assert.Len(t, row, 1)
		btn := row[0]
		assert.Equal(t, "it_toggle:"+strconv.Itoa(i), *btn.CallbackData,
			"button %d callback should be index-based, not the raw name", i)
	}

	// The currently-selected option must render with the ✅ prefix,
	// even though its name contains ':'.
	assert.Contains(t, kb.InlineKeyboard[1][0].Text, "API: REST")
	assert.Contains(t, kb.InlineKeyboard[1][0].Text, "✅")
}

func TestStatusKeyboard_NamesWithColonStayIntact(t *testing.T) {
	stateData := map[string]string{
		"project":  "PROJ",
		"kind":     "done",
		"nm_count": "2",
		"nm:0":     "Done",
		"nm:1":     "Verified: Stage 2",
		"sel:Done": "1",
	}

	kb := buildStatusKeyboard(locale.EN, stateData, statusKindDone)
	// 2 status buttons + 1 save/clear row.
	assert.Len(t, kb.InlineKeyboard, 3)
	assert.Equal(t, "ds_toggle:0", *kb.InlineKeyboard[0][0].CallbackData)
	assert.Equal(t, "ds_toggle:1", *kb.InlineKeyboard[1][0].CallbackData)
}
