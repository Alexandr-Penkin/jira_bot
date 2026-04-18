package eventsv1

import "strconv"

// LanguageChanged fires when a user picks a new UI language. Downstream
// projections keyed by TelegramID can update their locale cache without
// reading back from the preferences store.
type LanguageChanged struct {
	TelegramID int64  `json:"telegram_id"`
	Language   string `json:"language"`
	At         int64  `json:"at"`
}

func (LanguageChanged) Subject() string { return SubjectLanguageChanged }

func (e LanguageChanged) IdempotencyKey() string {
	return "preferences.language.changed:" + strconv.FormatInt(e.TelegramID, 10) +
		":" + strconv.FormatInt(e.At, 10)
}

// DefaultsChanged fires when any non-language preference changes. The
// payload is a full snapshot of the user's defaults so consumers can be
// simple (replace, don't merge) and don't need to know which setter was
// invoked.
type DefaultsChanged struct {
	TelegramID         int64    `json:"telegram_id"`
	DefaultProject     string   `json:"default_project,omitempty"`
	DefaultBoardID     int      `json:"default_board_id,omitempty"`
	SprintIssueTypes   []string `json:"sprint_issue_types,omitempty"`
	AssigneeFieldID    string   `json:"assignee_field_id,omitempty"`
	StoryPointsFieldID string   `json:"story_points_field_id,omitempty"`
	DoneStatuses       []string `json:"done_statuses,omitempty"`
	HoldStatuses       []string `json:"hold_statuses,omitempty"`
	DailyDoneJQL       string   `json:"daily_done_jql,omitempty"`
	DailyDoingJQL      string   `json:"daily_doing_jql,omitempty"`
	DailyPlanJQL       string   `json:"daily_plan_jql,omitempty"`
	At                 int64    `json:"at"`
}

func (*DefaultsChanged) Subject() string { return SubjectDefaultsChanged }

func (e *DefaultsChanged) IdempotencyKey() string {
	return "preferences.defaults.changed:" + strconv.FormatInt(e.TelegramID, 10) +
		":" + strconv.FormatInt(e.At, 10)
}
