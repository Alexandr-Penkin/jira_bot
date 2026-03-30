package webhook

type Event struct {
	WebhookEvent string     `json:"webhookEvent"`
	Timestamp    int64      `json:"timestamp"`
	Issue        *Issue     `json:"issue"`
	Comment      *Comment   `json:"comment"`
	Changelog    *Changelog `json:"changelog"`
	User         *User      `json:"user"`
}

type Issue struct {
	ID     string      `json:"id"`
	Key    string      `json:"key"`
	Self   string      `json:"self"`
	Fields IssueFields `json:"fields"`
}

type IssueFields struct {
	Summary   string   `json:"summary"`
	Status    *NameObj `json:"status"`
	Priority  *NameObj `json:"priority"`
	Assignee  *User    `json:"assignee"`
	Reporter  *User    `json:"reporter"`
	IssueType *NameObj `json:"issuetype"`
	Project   *Project `json:"project"`
}

type NameObj struct {
	Name string `json:"name"`
}

type Project struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type User struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
	Email       string `json:"emailAddress"`
}

type Comment struct {
	ID     string `json:"id"`
	Body   string `json:"body"`
	Author *User  `json:"author"`
}

type Changelog struct {
	Items []ChangelogItem `json:"items"`
}

type ChangelogItem struct {
	Field      string `json:"field"`
	FromString string `json:"fromString"`
	ToString   string `json:"toString"`
}

const (
	EventIssueCreated   = "jira:issue_created"
	EventIssueUpdated   = "jira:issue_updated"
	EventIssueDeleted   = "jira:issue_deleted"
	EventCommentCreated = "comment_created"
	EventCommentUpdated = "comment_updated"
)

// NormalizeEventType maps Jira webhook event names to our internal short names.
func NormalizeEventType(webhookEvent string) string {
	switch webhookEvent {
	case EventIssueCreated:
		return "issue_created"
	case EventIssueUpdated:
		return "issue_updated"
	case EventIssueDeleted:
		return "issue_deleted"
	case EventCommentCreated:
		return "comment_created"
	case EventCommentUpdated:
		return "comment_updated"
	default:
		return webhookEvent
	}
}

// AllEventTypes returns all supported event type short names.
func AllEventTypes() []string {
	return []string{
		"issue_created",
		"issue_updated",
		"issue_deleted",
		"comment_created",
		"comment_updated",
	}
}
