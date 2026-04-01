package poller

import (
	"context"
	"fmt"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/rs/zerolog"

	"SleepJiraBot/internal/format"
	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/notifydedup"
	"SleepJiraBot/internal/storage"
)

const (
	defaultPollInterval   = 30 * time.Second
	defaultBatchWindow    = 1 * time.Minute
	pollTimeout           = 30 * time.Second
	pollMaxResults        = 50
	mentionCommentMax     = 10
	mentionCommentTimeout = 10 * time.Second
)

// mergedChange tracks a field change, collapsing intermediate states
// (e.g. Status: Open → In Progress → Done becomes Status: Open → Done).
type mergedChange struct {
	Field      string
	FromString string
	ToString   string
}

// pendingNotification accumulates changes for a single issue
// destined for a specific chat before sending one merged notification.
type pendingNotification struct {
	chatID    int64
	issueKey  string
	siteURL   string
	issue     *jira.Issue
	authors   map[string]string        // accountID -> displayName
	changes   map[string]*mergedChange // field -> merged change
	firstSeen time.Time
}

// Poller periodically queries the Jira API for issue changes
// and sends notifications to subscribers. Changes for the same issue
// are accumulated during batchWindow before sending one merged notification.
// PollerStatus contains runtime status information for the admin panel.
type PollerStatus struct {
	Interval     time.Duration
	BatchWindow  time.Duration
	PendingCount int
	LastPollAt   time.Time
}

type Poller struct {
	subRepo     *storage.SubscriptionRepo
	userRepo    *storage.UserRepo
	jiraAPI     *jira.Client
	tgAPI       *tgbotapi.BotAPI
	log         zerolog.Logger
	interval    time.Duration
	batchWindow time.Duration
	dedup       *notifydedup.Guard
	pending     map[string]*pendingNotification
	lastPollAt  time.Time
}

func New(subRepo *storage.SubscriptionRepo, userRepo *storage.UserRepo, jiraAPI *jira.Client, tgAPI *tgbotapi.BotAPI, log zerolog.Logger, interval time.Duration, batchWindow time.Duration, dedup *notifydedup.Guard) *Poller {
	if interval <= 0 {
		interval = defaultPollInterval
	}
	if batchWindow <= 0 {
		batchWindow = defaultBatchWindow
	}
	return &Poller{
		subRepo:     subRepo,
		userRepo:    userRepo,
		jiraAPI:     jiraAPI,
		tgAPI:       tgAPI,
		log:         log,
		interval:    interval,
		batchWindow: batchWindow,
		dedup:       dedup,
		pending:     make(map[string]*pendingNotification),
	}
}

// Start begins the polling loop. It blocks until ctx is cancelled.
func (p *Poller) Start(ctx context.Context) {
	p.log.Info().
		Dur("interval", p.interval).
		Dur("batch_window", p.batchWindow).
		Msg("poller started")

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.flushAllPending()
			p.log.Info().Msg("poller stopped")
			return
		case <-ticker.C:
			p.poll(ctx)
			p.flushPending()
		}
	}
}

// Status returns the current poller status for admin monitoring.
func (p *Poller) Status() PollerStatus {
	return PollerStatus{
		Interval:     p.interval,
		BatchWindow:  p.batchWindow,
		PendingCount: len(p.pending),
		LastPollAt:   p.lastPollAt,
	}
}

func (p *Poller) poll(ctx context.Context) {
	p.lastPollAt = time.Now()

	// Get distinct user IDs from active subscriptions.
	userIDs, err := p.subRepo.GetActiveUserIDs(ctx)
	if err != nil {
		p.log.Error().Err(err).Msg("poller: failed to get subscription user IDs")
		return
	}

	for _, userID := range userIDs {
		subs, err := p.subRepo.GetActiveByUser(ctx, userID)
		if err != nil {
			p.log.Error().Err(err).Int64("user_id", userID).Msg("poller: failed to get user subscriptions")
			continue
		}
		p.pollUser(ctx, userID, subs)
	}
}

func (p *Poller) pollUser(ctx context.Context, telegramUserID int64, subs []storage.Subscription) {
	user, err := p.userRepo.GetByTelegramID(ctx, telegramUserID)
	if err != nil || user == nil || user.AccessToken == "" {
		return
	}

	now := time.Now().Unix()

	// Track notified issues per chat to avoid duplicates.
	notified := make(map[int64]map[string]bool)

	for i := range subs {
		sub := &subs[i]
		jql := p.buildJQL(sub)
		if jql == "" {
			continue
		}

		sinceTS := p.sinceTimestamp(sub)
		// JQL only supports minute precision, so truncate down for the query.
		// The exact-second filtering happens below using issue.Fields.Updated.
		sinceMinute := sinceTS - (sinceTS % 60)
		sinceStr := time.Unix(sinceMinute, 0).Format("2006-01-02 15:04")
		fullJQL := fmt.Sprintf("%s AND updated >= %q ORDER BY updated ASC", jql, sinceStr)

		pollCtx, cancel := context.WithTimeout(ctx, pollTimeout)
		result, err := p.jiraAPI.SearchIssuesWithChangelog(pollCtx, user, fullJQL, pollMaxResults)
		cancel()

		if err != nil {
			p.log.Error().Err(err).
				Str("sub_type", sub.SubscriptionType).
				Int64("user_id", telegramUserID).
				Msg("poller: failed to search issues")
			continue
		}

		sinceTime := time.Unix(sinceTS, 0)
		for j := range result.Issues {
			issue := &result.Issues[j]

			// JQL has minute precision so it returns issues from the
			// truncated window. Filter precisely using the issue's Updated
			// timestamp to skip issues already processed in prior cycles.
			if issue.Fields.Updated != "" {
				updatedAt, parseErr := time.Parse("2006-01-02T15:04:05.000-0700", issue.Fields.Updated)
				if parseErr == nil && !updatedAt.After(sinceTime) {
					continue
				}
			}

			// For mention subscriptions, only notify if user is actually mentioned in recent comments.
			if sub.SubscriptionType == storage.SubTypeMyMentions {
				if !p.isUserMentionedInComments(ctx, user, issue.Key, sinceTS) {
					continue
				}
			}

			// If already accumulating changes for this issue+chat, just merge.
			pendingKey := fmt.Sprintf("%d:%s", sub.TelegramChatID, issue.Key)
			if _, inPending := p.pending[pendingKey]; inPending {
				p.addPending(sub.TelegramChatID, issue, user.JiraSiteURL, sinceTS)
				continue
			}

			if notified[sub.TelegramChatID] == nil {
				notified[sub.TelegramChatID] = make(map[string]bool)
			}
			if notified[sub.TelegramChatID][issue.Key] {
				continue
			}
			notified[sub.TelegramChatID][issue.Key] = true

			if !p.dedup.Allow(sub.TelegramChatID, issue.Key) {
				p.log.Debug().
					Int64("chat_id", sub.TelegramChatID).
					Str("issue", issue.Key).
					Msg("poller: skipping duplicate notification")
				continue
			}

			p.addPending(sub.TelegramChatID, issue, user.JiraSiteURL, sinceTS)
		}

		if err := p.subRepo.UpdateLastPolled(ctx, sub.ID, now); err != nil {
			p.log.Error().Err(err).Msg("poller: failed to update last_polled_at")
		}
	}
}

func (p *Poller) buildJQL(sub *storage.Subscription) string {
	switch sub.SubscriptionType {
	case storage.SubTypeMyNewIssues:
		return "assignee = currentUser()"
	case storage.SubTypeMyMentions:
		// Jira doesn't have a direct "mentioned" JQL — we look for issues
		// where the current user is in the watcher list OR was mentioned
		// via text search. A practical approximation is "watcher = currentUser()"
		// combined with comment activity.
		return "watcher = currentUser()"
	case storage.SubTypeMyWatched:
		return "watcher = currentUser()"
	case storage.SubTypeProjectUpdates:
		if sub.JiraProjectKey == "" {
			return ""
		}
		return fmt.Sprintf("project = %s", sub.JiraProjectKey)
	case storage.SubTypeIssueUpdates:
		if sub.JiraIssueKey == "" {
			return ""
		}
		return fmt.Sprintf("key = %s", sub.JiraIssueKey)
	case storage.SubTypeFilterUpdates:
		if sub.JiraFilterJQL == "" {
			return ""
		}
		return sub.JiraFilterJQL
	default:
		return ""
	}
}

func (p *Poller) sinceTimestamp(sub *storage.Subscription) int64 {
	fallback := time.Now().Add(-p.interval).Unix()
	if sub.LastPolledAt > 0 {
		return sub.LastPolledAt
	}
	return fallback
}

func (p *Poller) addPending(chatID int64, issue *jira.Issue, siteURL string, sinceTS int64) {
	key := fmt.Sprintf("%d:%s", chatID, issue.Key)

	author, changes := recentChanges(issue, sinceTS)

	pn, exists := p.pending[key]
	if !exists {
		pn = &pendingNotification{
			chatID:    chatID,
			issueKey:  issue.Key,
			siteURL:   siteURL,
			issue:     issue,
			authors:   make(map[string]string),
			changes:   make(map[string]*mergedChange),
			firstSeen: time.Now(),
		}
		p.pending[key] = pn
	}

	// Always keep the latest issue state.
	pn.issue = issue

	if author != nil {
		pn.authors[author.AccountID] = author.DisplayName
	}

	// Merge changes: keep the original FromString, update ToString.
	for _, c := range changes {
		if existing, ok := pn.changes[c.Field]; ok {
			existing.ToString = c.ToString
		} else {
			pn.changes[c.Field] = &mergedChange{
				Field:      c.Field,
				FromString: c.FromString,
				ToString:   c.ToString,
			}
		}
	}
}

// flushPending sends notifications for issues that have been pending
// longer than batchWindow (no more changes expected).
func (p *Poller) flushPending() {
	now := time.Now()
	for key, pn := range p.pending {
		if now.Sub(pn.firstSeen) < p.batchWindow {
			continue
		}
		p.sendPendingNotification(pn)
		delete(p.pending, key)
	}
}

// flushAllPending sends all pending notifications (used on shutdown).
func (p *Poller) flushAllPending() {
	for key, pn := range p.pending {
		p.sendPendingNotification(pn)
		delete(p.pending, key)
	}
}

func (p *Poller) sendPendingNotification(pn *pendingNotification) {
	issueURL := fmt.Sprintf("%s/browse/%s", pn.siteURL, pn.issueKey)

	var sb strings.Builder

	// Collect author names.
	var authorNames []string
	for _, name := range pn.authors {
		authorNames = append(authorNames, name)
	}
	authorStr := "Someone"
	if len(authorNames) > 0 {
		authorStr = strings.Join(authorNames, ", ")
	}
	fmt.Fprintf(&sb, "👤 %s made updates in [%s](%s)\n",
		format.EscapeMarkdown(authorStr), pn.issueKey, issueURL)

	// Merged changes — skip fields that cancelled out.
	hasChanges := false
	for _, c := range pn.changes {
		if c.FromString != "" && c.FromString == c.ToString {
			continue
		}
		if !hasChanges {
			sb.WriteString("\n")
			hasChanges = true
		}
		if c.FromString != "" {
			fmt.Fprintf(&sb, "%s: %s → %s\n",
				format.EscapeMarkdown(c.Field),
				format.EscapeMarkdown(c.FromString),
				format.EscapeMarkdown(c.ToString))
		} else {
			fmt.Fprintf(&sb, "%s: %s\n",
				format.EscapeMarkdown(c.Field),
				format.EscapeMarkdown(c.ToString))
		}
	}

	issue := pn.issue
	sb.WriteString("\n")
	fmt.Fprintf(&sb, "Summary: %s\n", format.EscapeMarkdown(issue.Fields.Summary))
	if issue.Fields.Assignee != nil {
		fmt.Fprintf(&sb, "Assignee: %s\n", format.EscapeMarkdown(issue.Fields.Assignee.DisplayName))
	} else {
		sb.WriteString("Assignee: Unassigned\n")
	}
	if issue.Fields.Reporter != nil {
		fmt.Fprintf(&sb, "Reporter: %s\n", format.EscapeMarkdown(issue.Fields.Reporter.DisplayName))
	}
	if issue.Fields.Priority != nil {
		fmt.Fprintf(&sb, "Priority: %s\n", format.EscapeMarkdown(issue.Fields.Priority.Name))
	}
	if issue.Fields.IssueType != nil {
		fmt.Fprintf(&sb, "Type of issue: %s\n", format.EscapeMarkdown(issue.Fields.IssueType.Name))
	}
	if issue.Fields.Status != nil {
		fmt.Fprintf(&sb, "Status: %s\n", format.EscapeMarkdown(issue.Fields.Status.Name))
	}

	msg := tgbotapi.NewMessage(pn.chatID, sb.String())
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.DisableWebPagePreview = true

	if _, err := p.tgAPI.Send(msg); err != nil {
		p.log.Error().Err(err).Int64("chat_id", pn.chatID).Msg("poller: failed to send notification")
	}
}

// isUserMentionedInComments checks if the user's Jira account ID appears
// in recent comments of the given issue.
func (p *Poller) isUserMentionedInComments(ctx context.Context, user *storage.User, issueKey string, sinceTS int64) bool {
	if user.JiraAccountID == "" {
		return false
	}

	commentCtx, cancel := context.WithTimeout(ctx, mentionCommentTimeout)
	defer cancel()

	comments, err := p.jiraAPI.GetIssueComments(commentCtx, user, issueKey, mentionCommentMax)
	if err != nil {
		p.log.Debug().Err(err).Str("issue", issueKey).Msg("poller: failed to get comments for mention check")
		return false
	}

	sinceTime := time.Unix(sinceTS, 0)
	for i := range comments {
		comment := &comments[i]

		// Only check comments created/updated after the last poll.
		created, err := time.Parse("2006-01-02T15:04:05.000-0700", comment.Created)
		if err != nil {
			continue
		}
		if created.Before(sinceTime) {
			continue
		}

		// Check ADF body for mention nodes with user's account ID.
		for _, id := range comment.Body.ExtractMentionIDs() {
			if id == user.JiraAccountID {
				return true
			}
		}
	}

	return false
}

// recentChanges extracts changelog entries since the given timestamp.
// If no recent entries match but the changelog is non-empty, the author
// of the most recent history entry is returned as a fallback.
func recentChanges(issue *jira.Issue, sinceTS int64) (*jira.JiraUser, []jira.ChangeItem) {
	if issue.Changelog == nil {
		return nil, nil
	}

	sinceTime := time.Unix(sinceTS, 0)
	var author *jira.JiraUser
	var items []jira.ChangeItem

	for _, h := range issue.Changelog.Histories {
		created, err := time.Parse("2006-01-02T15:04:05.000-0700", h.Created)
		if err != nil {
			continue
		}
		if created.Before(sinceTime) {
			continue
		}
		if author == nil {
			author = h.Author
		}
		items = append(items, h.Items...)
	}

	// Fallback: if no recent entries matched, use the latest changelog
	// entry's author so we never show "Someone" when data is available.
	if author == nil && len(issue.Changelog.Histories) > 0 {
		last := issue.Changelog.Histories[len(issue.Changelog.Histories)-1]
		author = last.Author
	}

	return author, items
}
