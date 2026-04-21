// Package daily builds the multi-block ("done" / "doing" / "plan") daily
// standup text rendered both by the /daily command and the daily
// subscription scheduler. Keeping the logic here lets both call sites
// share JQL defaults, formatting, and Markdown escaping without one
// package depending on the other.
package daily

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"SleepJiraBot/internal/format"
	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/locale"
	"SleepJiraBot/internal/storage"
)

const maxResults = 50

// Searcher is the subset of jira.Client the builder relies on. Declared
// locally so tests can stub without pulling the full Jira client.
type Searcher interface {
	SearchIssues(ctx context.Context, user *storage.User, jql string, maxResults int) (*jira.SearchResult, error)
}

// Build produces the formatted daily standup text for the given user.
// displayName, when non-empty, is rendered in the header — used by the
// "daily for another user" flow. Returns an error only when all three
// Jira searches fail; a single failing block is degraded gracefully.
func Build(ctx context.Context, search Searcher, user *storage.User, lang locale.Lang, displayName string) (string, error) {
	doneJQL, doingJQL, planJQL := Queries(user)
	return BuildWithJQL(ctx, search, user, lang, displayName, doneJQL, doingJQL, planJQL)
}

// BuildWithJQL is the lower-level entry point: callers supply the three
// JQL queries explicitly. planJQL may be empty to omit the Plan block.
func BuildWithJQL(ctx context.Context, search Searcher, user *storage.User, lang locale.Lang, displayName, doneJQL, doingJQL, planJQL string) (string, error) {
	doneResult, doneErr := search.SearchIssues(ctx, user, doneJQL, maxResults)
	doingResult, doingErr := search.SearchIssues(ctx, user, doingJQL, maxResults)

	var planResult *jira.SearchResult
	var planErr error
	if planJQL != "" {
		planResult, planErr = search.SearchIssues(ctx, user, planJQL, maxResults)
	}

	if doneErr != nil && doingErr != nil && (planJQL == "" || planErr != nil) {
		return "", errors.Join(doneErr, doingErr, planErr)
	}

	return formatText(lang, user.JiraSiteURL, displayName, doneResult, doingResult, planResult), nil
}

// Queries returns the three JQLs used by the default /daily flow,
// applying user overrides stored on the User record.
func Queries(user *storage.User) (done, doing, plan string) {
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

	done = user.DailyDoneJQL
	if done == "" {
		done = fmt.Sprintf(
			"\"Developer[User Picker (single user)]\" = currentUser() AND updated >= %q AND status IN (Closed, \"Code review\", \"Ready for testing\", \"Ready for release\") ORDER BY updated DESC",
			yesterday,
		)
	}
	doing = user.DailyDoingJQL
	if doing == "" {
		doing = "\"Developer[User Picker (single user)]\" = currentUser() AND status = \"In Progress\" ORDER BY updated DESC"
	}
	plan = user.DailyPlanJQL
	if plan == "" {
		plan = "assignee = currentUser() AND status = \"Ready for development\" AND type IN (\"Dev Task\", Bug) ORDER BY updated DESC"
	}
	return done, doing, plan
}

func formatText(lang locale.Lang, siteURL, displayName string, done, doing, plan *jira.SearchResult) string {
	var sb strings.Builder

	if displayName != "" {
		fmt.Fprintf(&sb, "#daily (%s)\n", format.EscapeMarkdown(displayName))
	} else {
		sb.WriteString("#daily\n")
	}

	sb.WriteString("\n")
	sb.WriteString(locale.T(lang, "daily.done"))
	sb.WriteString(":\n")
	if done == nil || len(done.Issues) == 0 {
		sb.WriteString(locale.T(lang, "daily.no_done"))
		sb.WriteString("\n\n")
	} else {
		for i := range done.Issues {
			writeIssueLink(&sb, siteURL, &done.Issues[i])
		}
	}

	sb.WriteString("\n")
	sb.WriteString(locale.T(lang, "daily.doing"))
	sb.WriteString(":\n")
	if doing == nil || len(doing.Issues) == 0 {
		sb.WriteString(locale.T(lang, "daily.no_doing"))
		sb.WriteString("\n\n")
	} else {
		for i := range doing.Issues {
			writeIssueLink(&sb, siteURL, &doing.Issues[i])
		}
	}

	sb.WriteString("\n")
	sb.WriteString(locale.T(lang, "daily.plan"))
	sb.WriteString(":\n")
	if plan != nil && len(plan.Issues) > 0 {
		for i := range plan.Issues {
			writeIssueLink(&sb, siteURL, &plan.Issues[i])
		}
	} else if plan != nil {
		sb.WriteString(locale.T(lang, "daily.no_plan"))
		sb.WriteString("\n\n")
	}

	return sb.String()
}

func writeIssueLink(sb *strings.Builder, siteURL string, issue *jira.Issue) {
	issueURL := fmt.Sprintf("%s/browse/%s", siteURL, issue.Key)
	fmt.Fprintf(sb, "- [%s](%s) %s\n", issue.Key, issueURL, format.EscapeMarkdown(issue.Fields.Summary))
}
