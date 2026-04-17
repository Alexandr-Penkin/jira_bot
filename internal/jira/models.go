package jira

import "encoding/json"

// CreateMeta types for issue creation wizard.

type CreateMetaIssueType struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type CreateMetaField struct {
	FieldID         string            `json:"fieldId"`
	Key             string            `json:"key"`
	Name            string            `json:"name"`
	Required        bool              `json:"required"`
	HasDefaultValue bool              `json:"hasDefaultValue"`
	DefaultValue    json.RawMessage   `json:"defaultValue,omitempty"`
	Schema          CreateMetaSchema  `json:"schema"`
	AllowedValues   []CreateMetaValue `json:"allowedValues,omitempty"`
}

type CreateMetaSchema struct {
	Type     string `json:"type"`
	System   string `json:"system,omitempty"`
	Custom   string `json:"custom,omitempty"`
	CustomID int    `json:"customId,omitempty"`
}

type CreateMetaValue struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
}

type CreateIssueResponse struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Self string `json:"self"`
}

type JiraUser struct {
	AccountID   string            `json:"accountId"`
	DisplayName string            `json:"displayName"`
	Email       string            `json:"emailAddress"`
	AvatarURLs  map[string]string `json:"avatarUrls"`
	Active      bool              `json:"active"`
}

type Issue struct {
	ID        string      `json:"id"`
	Key       string      `json:"key"`
	Self      string      `json:"self"`
	Fields    IssueFields `json:"fields"`
	Changelog *Changelog  `json:"changelog,omitempty"`
}

type Changelog struct {
	Histories []ChangeHistory `json:"histories"`
}

type ChangeHistory struct {
	Author  *JiraUser    `json:"author"`
	Created string       `json:"created"`
	Items   []ChangeItem `json:"items"`
}

type ChangeItem struct {
	Field      string `json:"field"`
	FieldType  string `json:"fieldtype"`
	FieldID    string `json:"fieldId"`
	From       string `json:"from"`
	FromString string `json:"fromString"`
	To         string `json:"to"`
	ToString   string `json:"toString"`
}

type IssueFields struct {
	Summary            string       `json:"summary"`
	Description        *ADFDocument `json:"description"`
	Status             *Status      `json:"status"`
	Priority           *Priority    `json:"priority"`
	Assignee           *JiraUser    `json:"assignee"`
	Reporter           *JiraUser    `json:"reporter"`
	IssueType          *IssueType   `json:"issuetype"`
	Project            *Project     `json:"project"`
	Created            string       `json:"created"`
	Updated            string       `json:"updated"`
	DueDate            string       `json:"duedate"`
	Labels             []string     `json:"labels"`
	TimeSpent          int64        `json:"timespent,omitempty"`
	AggregateTimeSpent int64        `json:"aggregatetimespent,omitempty"`
	StoryPoints        *float64     `json:"-"`
	CustomAssignee     *JiraUser    `json:"-"`
}

type Project struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type Status struct {
	Name           string          `json:"name"`
	StatusCategory *StatusCategory `json:"statusCategory,omitempty"`
}

type StatusCategory struct {
	Key string `json:"key"` // "new", "indeterminate", "done"
}

type Priority struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type IssueType struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ADFDocument struct {
	Type    string    `json:"type"`
	Version int       `json:"version"`
	Content []ADFNode `json:"content"`
}

type ADFNode struct {
	Type    string         `json:"type"`
	Text    string         `json:"text,omitempty"`
	Attrs   map[string]any `json:"attrs,omitempty"`
	Content []ADFNode      `json:"content,omitempty"`
}

type SearchResult struct {
	StartAt    int     `json:"startAt"`
	MaxResults int     `json:"maxResults"`
	Total      int     `json:"total"`
	Issues     []Issue `json:"issues"`
}

type Transition struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	To   Status `json:"to"`
}

type TransitionsResponse struct {
	Transitions []Transition `json:"transitions"`
}

type Board struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Sprint struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	State        string `json:"state"` // active, closed, future
	StartDate    string `json:"startDate,omitempty"`
	EndDate      string `json:"endDate,omitempty"`
	CompleteDate string `json:"completeDate,omitempty"`
	Goal         string `json:"goal,omitempty"`
}

type Comment struct {
	ID      string       `json:"id"`
	Body    *ADFDocument `json:"body"`
	Author  *JiraUser    `json:"author"`
	Created string       `json:"created"`
	Updated string       `json:"updated"`
}

type CommentsResponse struct {
	StartAt    int       `json:"startAt"`
	MaxResults int       `json:"maxResults"`
	Total      int       `json:"total"`
	Comments   []Comment `json:"comments"`
}

type Filter struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	JQL  string `json:"jql"`
}

type JiraField struct {
	ID     string       `json:"id"`
	Name   string       `json:"name"`
	Custom bool         `json:"custom"`
	Schema *FieldSchema `json:"schema,omitempty"`
}

type FieldSchema struct {
	Type   string `json:"type"`
	System string `json:"system,omitempty"`
}

// ExtractText extracts plain text from an ADF document.
func (d *ADFDocument) ExtractText() string {
	if d == nil {
		return ""
	}
	var result string
	for _, node := range d.Content {
		result += extractNodeText(node)
	}
	return result
}

// ExtractMentionIDs extracts Jira account IDs from mention nodes in an ADF document.
func (d *ADFDocument) ExtractMentionIDs() []string {
	if d == nil {
		return nil
	}
	var ids []string
	for _, node := range d.Content {
		ids = append(ids, extractNodeMentions(node)...)
	}
	return ids
}

func extractNodeMentions(node ADFNode) []string {
	if node.Type == "mention" {
		// The canonical key is `id`, but some Jira payloads (older API
		// versions, inline cards) carry it under `accountId`. Accept
		// either so we don't miss legitimate mentions.
		if id, ok := node.Attrs["id"].(string); ok && id != "" {
			return []string{id}
		}
		if id, ok := node.Attrs["accountId"].(string); ok && id != "" {
			return []string{id}
		}
	}
	var ids []string
	for _, child := range node.Content {
		ids = append(ids, extractNodeMentions(child)...)
	}
	return ids
}

func extractNodeText(node ADFNode) string {
	if node.Text != "" {
		return node.Text
	}
	var result string
	for _, child := range node.Content {
		result += extractNodeText(child)
	}
	if node.Type == "paragraph" || node.Type == "heading" {
		result += "\n"
	}
	return result
}
