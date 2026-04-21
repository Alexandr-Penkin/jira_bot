// Package preferencesclient provides an HTTP client for the Phase-5
// preferences-svc. A Client instance is safe for concurrent use and
// implements the same surface as preferences.LocalProvider so the two
// are interchangeable behind the preferences.Provider interface.
package preferencesclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	preferencesv1 "SleepJiraBot/pkg/preferencesv1"
)

// DefaultTimeout bounds a single preferences HTTP call.
const DefaultTimeout = 5 * time.Second

type Client struct {
	baseURL   string
	authToken string
	http      *http.Client
}

// New constructs a Client. When httpClient is nil, a client with
// DefaultTimeout and no proxy is used. The authToken is sent as a
// bearer credential — pass "" to call an unauthenticated server.
func New(baseURL, authToken string, httpClient *http.Client) (*Client, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("preferencesclient: baseURL required")
	}
	if _, err := url.Parse(baseURL); err != nil {
		return nil, fmt.Errorf("preferencesclient: invalid baseURL: %w", err)
	}
	if httpClient == nil {
		// Wrap the default transport with otelhttp so preferences-svc
		// calls show up as child spans of the caller's request context
		// and feed the otelhttp client-duration histogram. Callers that
		// pass their own client own their instrumentation.
		httpClient = &http.Client{
			Timeout:   DefaultTimeout,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		}
	}
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		authToken: authToken,
		http:      httpClient,
	}, nil
}

func (c *Client) Get(ctx context.Context, telegramID int64) (*preferencesv1.Preferences, error) {
	u := c.baseURL + preferencesv1.GetPath + "?telegram_id=" + strconv.FormatInt(telegramID, 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, err
	}
	c.setAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call preferences-svc: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, fmt.Errorf("read preferences-svc response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, parseError(resp.StatusCode, raw)
	}

	var prefs preferencesv1.Preferences
	if err := json.Unmarshal(raw, &prefs); err != nil {
		return nil, fmt.Errorf("decode preferences-svc response: %w", err)
	}
	return &prefs, nil
}

func (c *Client) SetLanguage(ctx context.Context, telegramID int64, lang string) error {
	return c.post(ctx, preferencesv1.SetLanguagePath, preferencesv1.SetLanguageRequest{
		TelegramID: telegramID,
		Language:   lang,
	})
}

func (c *Client) SetDefaults(ctx context.Context, telegramID int64, project string, boardID int) error {
	return c.post(ctx, preferencesv1.SetDefaultsPath, preferencesv1.SetDefaultsRequest{
		TelegramID:     telegramID,
		DefaultProject: project,
		DefaultBoardID: boardID,
	})
}

func (c *Client) SetDefaultIssueType(ctx context.Context, telegramID int64, typeID, typeName string) error {
	return c.post(ctx, preferencesv1.SetDefaultIssueTypePath, preferencesv1.SetDefaultIssueTypeRequest{
		TelegramID:           telegramID,
		DefaultIssueTypeID:   typeID,
		DefaultIssueTypeName: typeName,
	})
}

func (c *Client) SetSprintIssueTypes(ctx context.Context, telegramID int64, issueTypes []string) error {
	return c.post(ctx, preferencesv1.SetSprintIssueTypesPath, preferencesv1.SetSprintIssueTypesRequest{
		TelegramID: telegramID,
		IssueTypes: issueTypes,
	})
}

func (c *Client) SetDoneStatuses(ctx context.Context, telegramID int64, statuses []string) error {
	return c.post(ctx, preferencesv1.SetDoneStatusesPath, preferencesv1.SetStatusesRequest{
		TelegramID: telegramID,
		Statuses:   statuses,
	})
}

func (c *Client) SetHoldStatuses(ctx context.Context, telegramID int64, statuses []string) error {
	return c.post(ctx, preferencesv1.SetHoldStatusesPath, preferencesv1.SetStatusesRequest{
		TelegramID: telegramID,
		Statuses:   statuses,
	})
}

func (c *Client) SetAssigneeField(ctx context.Context, telegramID int64, fieldID string) error {
	return c.post(ctx, preferencesv1.SetAssigneeFieldPath, preferencesv1.SetFieldRequest{
		TelegramID: telegramID,
		FieldID:    fieldID,
	})
}

func (c *Client) SetStoryPointsField(ctx context.Context, telegramID int64, fieldID string) error {
	return c.post(ctx, preferencesv1.SetStoryPointsFieldPath, preferencesv1.SetFieldRequest{
		TelegramID: telegramID,
		FieldID:    fieldID,
	})
}

func (c *Client) SetDailyJQL(ctx context.Context, telegramID int64, doneJQL, doingJQL, planJQL string) error {
	return c.post(ctx, preferencesv1.SetDailyJQLPath, preferencesv1.SetDailyJQLRequest{
		TelegramID: telegramID,
		DoneJQL:    doneJQL,
		DoingJQL:   doingJQL,
		PlanJQL:    planJQL,
	})
}

func (c *Client) post(ctx context.Context, path string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("call preferences-svc: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 400 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return parseError(resp.StatusCode, raw)
}

func (c *Client) setAuth(req *http.Request) {
	if c.authToken != "" {
		req.Header.Set(preferencesv1.AuthHeader, preferencesv1.AuthScheme+c.authToken)
	}
}

func parseError(status int, body []byte) error {
	var errResp preferencesv1.ErrorResponse
	_ = json.Unmarshal(body, &errResp)
	return &Error{Status: status, Code: errResp.Code, Message: errResp.Message}
}

// Error is returned for any non-2xx response from preferences-svc.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("preferences-svc %d %s: %s", e.Status, e.Code, e.Message)
}

// IsNotFound is a convenience for callers that want to treat "user has
// no preferences yet" as a non-error.
func IsNotFound(err error) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Code == preferencesv1.ErrCodeNotFound
}
