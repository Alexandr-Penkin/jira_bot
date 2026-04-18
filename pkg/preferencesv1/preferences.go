// Package preferencesv1 defines the v1 wire protocol for the Phase-5
// preferences-svc. The surface exposes a single aggregate — user
// preferences keyed by TelegramID — with a GET for reads and targeted
// POST endpoints mirroring the UserRepo setter methods in the monolith.
//
// Contract: consumers GET /internal/preferences?telegram_id=X to read a
// full snapshot (zero values when the user has no record). Setter
// endpoints accept a TelegramID in the body and the field(s) being
// updated; on success the server publishes the corresponding domain
// event (preferences.language.changed.v1 or preferences.defaults.changed.v1).
//
// Auth uses the same Authorization: Bearer <token> scheme as identity-svc
// (shared secret carried in INTERNAL_AUTH_TOKEN). Empty token disables
// auth; the listener is expected to be on an internal-only network.
package preferencesv1

const (
	// GetPath returns the current preferences snapshot.
	GetPath = "/internal/preferences"

	// SetLanguagePath updates the language field.
	SetLanguagePath = "/internal/preferences/language"

	// SetDefaultsPath updates default_project + default_board_id.
	SetDefaultsPath = "/internal/preferences/defaults"

	// SetSprintIssueTypesPath updates sprint_issue_types.
	SetSprintIssueTypesPath = "/internal/preferences/sprint-issue-types"

	// SetDoneStatusesPath updates done_statuses.
	SetDoneStatusesPath = "/internal/preferences/done-statuses"

	// SetHoldStatusesPath updates hold_statuses.
	SetHoldStatusesPath = "/internal/preferences/hold-statuses"

	// SetAssigneeFieldPath updates assignee_field_id.
	SetAssigneeFieldPath = "/internal/preferences/assignee-field"

	// SetStoryPointsFieldPath updates story_points_field_id.
	SetStoryPointsFieldPath = "/internal/preferences/story-points-field"

	// SetDailyJQLPath updates daily_done_jql + daily_doing_jql + daily_plan_jql.
	SetDailyJQLPath = "/internal/preferences/daily-jql"

	AuthHeader = "Authorization"
	AuthScheme = "Bearer "
)

const (
	ErrCodeNotFound       = "not_found"
	ErrCodeInvalidRequest = "invalid_request"
	ErrCodeUnauthorized   = "unauthorized"
	ErrCodeInternal       = "internal"
)

// Preferences is the GET response. Zero values indicate the user has not
// configured that preference; consumers treat them as "default".
type Preferences struct {
	TelegramID         int64    `json:"telegram_id"`
	Language           string   `json:"language,omitempty"`
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
}

// SetLanguageRequest — body for SetLanguagePath.
type SetLanguageRequest struct {
	TelegramID int64  `json:"telegram_id"`
	Language   string `json:"language"`
}

// SetDefaultsRequest — body for SetDefaultsPath.
type SetDefaultsRequest struct {
	TelegramID     int64  `json:"telegram_id"`
	DefaultProject string `json:"default_project"`
	DefaultBoardID int    `json:"default_board_id"`
}

// SetSprintIssueTypesRequest — body for SetSprintIssueTypesPath.
type SetSprintIssueTypesRequest struct {
	TelegramID int64    `json:"telegram_id"`
	IssueTypes []string `json:"issue_types"`
}

// SetStatusesRequest — shared shape for done/hold setters.
type SetStatusesRequest struct {
	TelegramID int64    `json:"telegram_id"`
	Statuses   []string `json:"statuses"`
}

// SetFieldRequest — shared shape for assignee/story-points setters.
type SetFieldRequest struct {
	TelegramID int64  `json:"telegram_id"`
	FieldID    string `json:"field_id"`
}

// SetDailyJQLRequest — body for SetDailyJQLPath.
type SetDailyJQLRequest struct {
	TelegramID int64  `json:"telegram_id"`
	DoneJQL    string `json:"done_jql"`
	DoingJQL   string `json:"doing_jql"`
	PlanJQL    string `json:"plan_jql"`
}

// ErrorResponse is returned with non-2xx statuses.
type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}
