package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"SleepJiraBot/internal/storage"
)

const (
	apiBaseURL   = "https://api.atlassian.com/ex/jira/%s/rest/api/3"
	agileBaseURL = "https://api.atlassian.com/ex/jira/%s/rest/agile/1.0"

	maxResponseSize = 10 << 20 // 10 MB
)

var httpClient = &http.Client{
	Timeout: 30 * time.Second,
}

type tokenLockEntry struct {
	mu       *sync.Mutex
	lastUsed time.Time
}

type Client struct {
	oauth      *OAuthClient
	userRepo   *storage.UserRepo
	log        zerolog.Logger
	locksMu    sync.Mutex
	tokenLocks map[int64]*tokenLockEntry
}

func NewClient(oauth *OAuthClient, userRepo *storage.UserRepo, log zerolog.Logger) *Client {
	return &Client{
		oauth:      oauth,
		userRepo:   userRepo,
		log:        log,
		tokenLocks: make(map[int64]*tokenLockEntry),
	}
}

// StartCleanup starts a background goroutine that removes stale token locks.
func (c *Client) StartCleanup(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.cleanTokenLocks()
			}
		}
	}()
}

func (c *Client) cleanTokenLocks() {
	c.locksMu.Lock()
	defer c.locksMu.Unlock()

	now := time.Now()
	for id, entry := range c.tokenLocks {
		if now.Sub(entry.lastUsed) > 30*time.Minute {
			delete(c.tokenLocks, id)
		}
	}
}

func (c *Client) getUserTokenLock(telegramUserID int64) *sync.Mutex {
	c.locksMu.Lock()
	defer c.locksMu.Unlock()

	entry, ok := c.tokenLocks[telegramUserID]
	if !ok {
		entry = &tokenLockEntry{mu: &sync.Mutex{}}
		c.tokenLocks[telegramUserID] = entry
	}
	entry.lastUsed = time.Now()
	return entry.mu
}

func (c *Client) GetMyself(ctx context.Context, user *storage.User) (*JiraUser, error) {
	body, err := c.doRequest(ctx, user, http.MethodGet, "/myself", nil)
	if err != nil {
		return nil, err
	}

	var jiraUser JiraUser
	if err = json.Unmarshal(body, &jiraUser); err != nil {
		return nil, fmt.Errorf("decode jira user: %w", err)
	}

	return &jiraUser, nil
}

func (c *Client) GetIssue(ctx context.Context, user *storage.User, issueKey string) (*Issue, error) {
	path := fmt.Sprintf("/issue/%s", url.PathEscape(issueKey))
	body, err := c.doRequest(ctx, user, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var issue Issue
	if err = json.Unmarshal(body, &issue); err != nil {
		return nil, fmt.Errorf("decode issue: %w", err)
	}

	return &issue, nil
}

func (c *Client) SearchIssues(ctx context.Context, user *storage.User, jql string, maxResults int) (*SearchResult, error) {
	params := url.Values{
		"jql":        {jql},
		"maxResults": {fmt.Sprintf("%d", maxResults)},
		"fields":     {"summary,status,priority,assignee,issuetype,duedate,project"},
	}
	path := "/search/jql?" + params.Encode()

	body, err := c.doRequest(ctx, user, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var result SearchResult
	if err = json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode search result: %w", err)
	}

	return &result, nil
}

// SearchIssuesWithStoryPoints searches issues and extracts story points from custom fields.
// If assigneeFieldID is not empty, it also extracts the custom assignee field.
func (c *Client) SearchIssuesWithStoryPoints(ctx context.Context, user *storage.User, jql string, maxResults int, assigneeFieldID string) (*SearchResult, error) {
	fields := "summary,status,priority,assignee,issuetype,duedate,project,story_points,customfield_10016"
	if assigneeFieldID != "" {
		fields += "," + assigneeFieldID
	}
	params := url.Values{
		"jql":        {jql},
		"maxResults": {fmt.Sprintf("%d", maxResults)},
		"fields":     {fields},
	}
	path := "/search/jql?" + params.Encode()

	body, err := c.doRequest(ctx, user, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var raw struct {
		StartAt    int `json:"startAt"`
		MaxResults int `json:"maxResults"`
		Total      int `json:"total"`
		Issues     []struct {
			ID     string          `json:"id"`
			Key    string          `json:"key"`
			Self   string          `json:"self"`
			Fields json.RawMessage `json:"fields"`
		} `json:"issues"`
	}
	if err = json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode search result: %w", err)
	}

	result := &SearchResult{
		StartAt:    raw.StartAt,
		MaxResults: raw.MaxResults,
		Total:      raw.Total,
		Issues:     make([]Issue, len(raw.Issues)),
	}

	for i, ri := range raw.Issues {
		result.Issues[i].ID = ri.ID
		result.Issues[i].Key = ri.Key
		result.Issues[i].Self = ri.Self

		if err = json.Unmarshal(ri.Fields, &result.Issues[i].Fields); err != nil {
			return nil, fmt.Errorf("decode issue fields: %w", err)
		}

		var extra map[string]json.RawMessage
		if err = json.Unmarshal(ri.Fields, &extra); err == nil {
			result.Issues[i].Fields.StoryPoints = extractStoryPoints(extra)
			if assigneeFieldID != "" {
				result.Issues[i].Fields.CustomAssignee = extractUserField(extra, assigneeFieldID)
			}
		}
	}

	return result, nil
}

func extractStoryPoints(fields map[string]json.RawMessage) *float64 {
	for _, key := range []string{"story_points", "customfield_10016"} {
		raw, ok := fields[key]
		if !ok || string(raw) == "null" {
			continue
		}
		var sp float64
		if json.Unmarshal(raw, &sp) == nil {
			return &sp
		}
	}
	return nil
}

func extractUserField(fields map[string]json.RawMessage, fieldID string) *JiraUser {
	raw, ok := fields[fieldID]
	if !ok || string(raw) == "null" {
		return nil
	}
	var user JiraUser
	if json.Unmarshal(raw, &user) == nil && user.AccountID != "" {
		return &user
	}
	return nil
}

func (c *Client) GetFields(ctx context.Context, user *storage.User) ([]JiraField, error) {
	body, err := c.doRequest(ctx, user, http.MethodGet, "/field", nil)
	if err != nil {
		return nil, err
	}

	var fields []JiraField
	if err = json.Unmarshal(body, &fields); err != nil {
		return nil, fmt.Errorf("decode fields: %w", err)
	}

	return fields, nil
}

// GetIssueComments returns comments for an issue, ordered by creation date descending.
// If since is non-empty, only comments created after this date are returned (format: "2006-01-02 15:04").
func (c *Client) GetIssueComments(ctx context.Context, user *storage.User, issueKey string, maxResults int) ([]Comment, error) {
	params := url.Values{
		"maxResults": {fmt.Sprintf("%d", maxResults)},
		"orderBy":    {"-created"},
	}
	path := fmt.Sprintf("/issue/%s/comment?%s", url.PathEscape(issueKey), params.Encode())
	body, err := c.doRequest(ctx, user, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var resp CommentsResponse
	if err = json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode comments: %w", err)
	}

	return resp.Comments, nil
}

func (c *Client) AddComment(ctx context.Context, user *storage.User, issueKey, text string) error {
	payload := map[string]interface{}{
		"body": map[string]interface{}{
			"type":    "doc",
			"version": 1,
			"content": []map[string]interface{}{
				{
					"type": "paragraph",
					"content": []map[string]interface{}{
						{
							"type": "text",
							"text": text,
						},
					},
				},
			},
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal comment: %w", err)
	}

	path := fmt.Sprintf("/issue/%s/comment", url.PathEscape(issueKey))
	_, err = c.doRequest(ctx, user, http.MethodPost, path, bytes.NewReader(data))
	return err
}

func (c *Client) GetTransitions(ctx context.Context, user *storage.User, issueKey string) ([]Transition, error) {
	path := fmt.Sprintf("/issue/%s/transitions", url.PathEscape(issueKey))
	body, err := c.doRequest(ctx, user, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var resp TransitionsResponse
	if err = json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode transitions: %w", err)
	}

	return resp.Transitions, nil
}

func (c *Client) DoTransition(ctx context.Context, user *storage.User, issueKey, transitionID string) error {
	payload := map[string]interface{}{
		"transition": map[string]string{
			"id": transitionID,
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal transition: %w", err)
	}

	path := fmt.Sprintf("/issue/%s/transitions", url.PathEscape(issueKey))
	_, err = c.doRequest(ctx, user, http.MethodPost, path, bytes.NewReader(data))
	return err
}

func (c *Client) AssignIssue(ctx context.Context, user *storage.User, issueKey, accountID string) error {
	payload := map[string]interface{}{
		"accountId": accountID,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal assignee: %w", err)
	}

	path := fmt.Sprintf("/issue/%s/assignee", url.PathEscape(issueKey))
	_, err = c.doRequest(ctx, user, http.MethodPut, path, bytes.NewReader(data))
	return err
}

// SearchIssuesForSprintReport searches issues with both story points and changelog.
// Combines the functionality of SearchIssuesWithStoryPoints and SearchIssuesWithChangelog.
func (c *Client) SearchIssuesForSprintReport(ctx context.Context, user *storage.User, jql string, maxResults int, assigneeFieldID string) (*SearchResult, error) {
	fields := "summary,status,priority,assignee,issuetype,duedate,project,story_points,customfield_10016"
	if assigneeFieldID != "" {
		fields += "," + assigneeFieldID
	}
	params := url.Values{
		"jql":        {jql},
		"maxResults": {fmt.Sprintf("%d", maxResults)},
		"fields":     {fields},
		"expand":     {"changelog"},
	}
	path := "/search/jql?" + params.Encode()

	body, err := c.doRequest(ctx, user, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var raw struct {
		StartAt    int `json:"startAt"`
		MaxResults int `json:"maxResults"`
		Total      int `json:"total"`
		Issues     []struct {
			ID        string          `json:"id"`
			Key       string          `json:"key"`
			Self      string          `json:"self"`
			Fields    json.RawMessage `json:"fields"`
			Changelog *Changelog      `json:"changelog,omitempty"`
		} `json:"issues"`
	}
	if err = json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode search result: %w", err)
	}

	result := &SearchResult{
		StartAt:    raw.StartAt,
		MaxResults: raw.MaxResults,
		Total:      raw.Total,
		Issues:     make([]Issue, len(raw.Issues)),
	}

	for i, ri := range raw.Issues {
		result.Issues[i].ID = ri.ID
		result.Issues[i].Key = ri.Key
		result.Issues[i].Self = ri.Self
		result.Issues[i].Changelog = ri.Changelog

		if err = json.Unmarshal(ri.Fields, &result.Issues[i].Fields); err != nil {
			return nil, fmt.Errorf("decode issue fields: %w", err)
		}

		var extra map[string]json.RawMessage
		if err = json.Unmarshal(ri.Fields, &extra); err == nil {
			result.Issues[i].Fields.StoryPoints = extractStoryPoints(extra)
			if assigneeFieldID != "" {
				result.Issues[i].Fields.CustomAssignee = extractUserField(extra, assigneeFieldID)
			}
		}
	}

	return result, nil
}

// SearchIssuesWithChangelog searches issues and includes recent changelog.
func (c *Client) SearchIssuesWithChangelog(ctx context.Context, user *storage.User, jql string, maxResults int) (*SearchResult, error) {
	params := url.Values{
		"jql":        {jql},
		"maxResults": {fmt.Sprintf("%d", maxResults)},
		"fields":     {"summary,status,priority,assignee,reporter,issuetype,project"},
		"expand":     {"changelog"},
	}
	path := "/search/jql?" + params.Encode()

	body, err := c.doRequest(ctx, user, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var result SearchResult
	if err = json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode search result: %w", err)
	}

	return &result, nil
}

// --- Agile API methods ---

const (
	boardPageSize  = 50
	sprintPageSize = 50
)

// GetBoards returns all boards for a project using the Agile API (paginated).
func (c *Client) GetBoards(ctx context.Context, user *storage.User, projectKey string) ([]Board, error) {
	var all []Board
	startAt := 0

	for {
		params := url.Values{
			"maxResults": {fmt.Sprintf("%d", boardPageSize)},
			"startAt":    {fmt.Sprintf("%d", startAt)},
		}
		if projectKey != "" {
			params.Set("projectKeyOrId", projectKey)
		}
		path := "/board?" + params.Encode()

		body, err := c.doAgileRequest(ctx, user, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}

		var result struct {
			Values []Board `json:"values"`
			IsLast bool    `json:"isLast"`
		}
		if err = json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("decode boards: %w", err)
		}

		all = append(all, result.Values...)

		if result.IsLast || len(result.Values) == 0 {
			break
		}
		startAt += len(result.Values)
	}

	return all, nil
}

// GetSprint returns a single sprint by ID.
func (c *Client) GetSprint(ctx context.Context, user *storage.User, sprintID int) (*Sprint, error) {
	path := fmt.Sprintf("/sprint/%d", sprintID)
	body, err := c.doAgileRequest(ctx, user, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var sprint Sprint
	if err = json.Unmarshal(body, &sprint); err != nil {
		return nil, fmt.Errorf("decode sprint: %w", err)
	}
	return &sprint, nil
}

// GetSprints returns active and closed sprints for a board (paginated),
// sorted by start date descending (newest first).
func (c *Client) GetSprints(ctx context.Context, user *storage.User, boardID int) ([]Sprint, error) {
	var all []Sprint
	startAt := 0

	for {
		params := url.Values{
			"maxResults": {fmt.Sprintf("%d", sprintPageSize)},
			"startAt":    {fmt.Sprintf("%d", startAt)},
			"state":      {"active,closed"},
		}
		path := fmt.Sprintf("/board/%d/sprint?%s", boardID, params.Encode())

		body, err := c.doAgileRequest(ctx, user, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}

		var result struct {
			Values []Sprint `json:"values"`
			IsLast bool     `json:"isLast"`
		}
		if err = json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("decode sprints: %w", err)
		}

		all = append(all, result.Values...)

		if result.IsLast || len(result.Values) == 0 {
			break
		}
		startAt += len(result.Values)
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].StartDate > all[j].StartDate
	})

	return all, nil
}

func (c *Client) doAgileRequest(ctx context.Context, user *storage.User, method, path string, reqBody io.Reader) ([]byte, error) {
	accessToken, err := c.ensureValidToken(ctx, user)
	if err != nil {
		return nil, fmt.Errorf("ensure valid token: %w", err)
	}

	apiURL := fmt.Sprintf(agileBaseURL, user.JiraCloudID) + path
	req, err := http.NewRequestWithContext(ctx, method, apiURL, reqBody)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("jira agile API %s %s: %d %s", method, path, resp.StatusCode, string(body))
	}

	return body, nil
}

// GetProjectIssueTypes returns available issue types for a project.
func (c *Client) GetProjectIssueTypes(ctx context.Context, user *storage.User, projectKey string) ([]IssueType, error) {
	path := fmt.Sprintf("/project/%s", url.PathEscape(projectKey))
	body, err := c.doRequest(ctx, user, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var project struct {
		IssueTypes []IssueType `json:"issueTypes"`
	}
	if err = json.Unmarshal(body, &project); err != nil {
		return nil, fmt.Errorf("decode project: %w", err)
	}

	return project.IssueTypes, nil
}

func (c *Client) GetMyFilters(ctx context.Context, user *storage.User) ([]Filter, error) {
	body, err := c.doRequest(ctx, user, http.MethodGet, "/filter/my", nil)
	if err != nil {
		return nil, err
	}

	var filters []Filter
	if err = json.Unmarshal(body, &filters); err != nil {
		return nil, fmt.Errorf("decode filters: %w", err)
	}

	return filters, nil
}

func (c *Client) GetFavouriteFilters(ctx context.Context, user *storage.User) ([]Filter, error) {
	body, err := c.doRequest(ctx, user, http.MethodGet, "/filter/favourite", nil)
	if err != nil {
		return nil, err
	}

	var filters []Filter
	if err = json.Unmarshal(body, &filters); err != nil {
		return nil, fmt.Errorf("decode favourite filters: %w", err)
	}

	return filters, nil
}

func (c *Client) SearchUsers(ctx context.Context, user *storage.User, query string, maxResults int) ([]JiraUser, error) {
	params := url.Values{
		"query":      {query},
		"maxResults": {fmt.Sprintf("%d", maxResults)},
	}
	path := "/user/search?" + params.Encode()

	body, err := c.doRequest(ctx, user, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var users []JiraUser
	if err = json.Unmarshal(body, &users); err != nil {
		return nil, fmt.Errorf("decode user search: %w", err)
	}

	return users, nil
}

func (c *Client) doRequest(ctx context.Context, user *storage.User, method, path string, reqBody io.Reader) ([]byte, error) {
	accessToken, err := c.ensureValidToken(ctx, user)
	if err != nil {
		return nil, fmt.Errorf("ensure valid token: %w", err)
	}

	apiURL := fmt.Sprintf(apiBaseURL, user.JiraCloudID) + path
	req, err := http.NewRequestWithContext(ctx, method, apiURL, reqBody)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("jira API %s %s: %d %s", method, path, resp.StatusCode, string(body))
	}

	return body, nil
}

// ensureValidToken checks whether the user's token is still valid and refreshes
// it if needed. It returns the current access token for the caller to use,
// avoiding a race where another goroutine might be mutating user fields.
func (c *Client) ensureValidToken(ctx context.Context, user *storage.User) (string, error) {
	if time.Now().Before(user.TokenExpiresAt.Add(-60 * time.Second)) {
		return user.AccessToken, nil
	}

	mu := c.getUserTokenLock(user.TelegramUserID)
	mu.Lock()
	defer mu.Unlock()

	// Re-read from DB after acquiring lock — another goroutine may have already refreshed.
	fresh, err := c.userRepo.GetByTelegramID(ctx, user.TelegramUserID)
	if err != nil {
		return "", fmt.Errorf("re-read user: %w", err)
	}
	if fresh == nil {
		return "", fmt.Errorf("user %d not found after lock", user.TelegramUserID)
	}

	if time.Now().Before(fresh.TokenExpiresAt.Add(-60 * time.Second)) {
		return fresh.AccessToken, nil
	}

	c.log.Debug().Int64("telegram_user_id", user.TelegramUserID).Msg("refreshing jira token")

	tokenResp, err := c.oauth.RefreshTokens(ctx, fresh.RefreshToken)
	if err != nil {
		return "", fmt.Errorf("refresh token: %w", err)
	}

	newAccessToken := tokenResp.AccessToken
	newRefreshToken := fresh.RefreshToken
	if tokenResp.RefreshToken != "" {
		newRefreshToken = tokenResp.RefreshToken
	}
	newExpiresAt := c.oauth.TokenExpiresAt(tokenResp.ExpiresIn)

	if err = c.userRepo.UpdateTokens(ctx, user.TelegramUserID, newAccessToken, newRefreshToken, newExpiresAt); err != nil {
		return "", fmt.Errorf("save refreshed tokens: %w", err)
	}

	return newAccessToken, nil
}
