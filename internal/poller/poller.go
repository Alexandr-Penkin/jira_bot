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
	defaultPollInterval   = 2 * time.Minute
	pollTimeout           = 30 * time.Second
	pollMaxResults        = 50
	mentionCommentMax     = 10
	mentionCommentTimeout = 10 * time.Second
)

// Poller periodically queries the Jira API for issue changes
// and sends notifications to subscribers.
type Poller struct {
	subRepo  *storage.SubscriptionRepo
	userRepo *storage.UserRepo
	jiraAPI  *jira.Client
	tgAPI    *tgbotapi.BotAPI
	log      zerolog.Logger
	interval time.Duration
	dedup    *notifydedup.Guard
}

func New(subRepo *storage.SubscriptionRepo, userRepo *storage.UserRepo, jiraAPI *jira.Client, tgAPI *tgbotapi.BotAPI, log zerolog.Logger, interval time.Duration, dedup *notifydedup.Guard) *Poller {
	if interval <= 0 {
		interval = defaultPollInterval
	}
	return &Poller{
		subRepo:  subRepo,
		userRepo: userRepo,
		jiraAPI:  jiraAPI,
		tgAPI:    tgAPI,
		log:      log,
		interval: interval,
		dedup:    dedup,
	}
}

// Start begins the polling loop. It blocks until ctx is cancelled.
func (p *Poller) Start(ctx context.Context) {
	p.log.Info().Dur("interval", p.interval).Msg("poller started")

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.log.Info().Msg("poller stopped")
			return
		case <-ticker.C:
			p.poll(ctx)
		}
	}
}

func (p *Poller) poll(ctx context.Context) {
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

		since := p.sinceTimestamp(sub)
		// Truncate to minute boundary so JQL and changelog filtering use the
		// same precision. JQL only supports minute-level timestamps, so without
		// this the changelog filter can be stricter and discard valid entries.
		since = since - (since % 60)
		sinceStr := time.Unix(since, 0).Format("2006-01-02 15:04")
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

		for j := range result.Issues {
			issue := &result.Issues[j]

			// For mention subscriptions, only notify if user is actually mentioned in recent comments.
			if sub.SubscriptionType == storage.SubTypeMyMentions {
				if !p.isUserMentionedInComments(ctx, user, issue.Key, since) {
					continue
				}
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

			p.notifySubscription(sub, issue, user.JiraSiteURL, since)
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

func (p *Poller) notifySubscription(sub *storage.Subscription, issue *jira.Issue, siteURL string, sinceTS int64) {
	issueURL := fmt.Sprintf("%s/browse/%s", siteURL, issue.Key)

	author, changes := recentChanges(issue, sinceTS)

	var sb strings.Builder

	authorName := "Someone"
	if author != nil {
		authorName = author.DisplayName
	}
	fmt.Fprintf(&sb, "👤 %s made updates in [%s](%s)\n",
		format.EscapeMarkdown(authorName), issue.Key, issueURL)

	if len(changes) > 0 {
		sb.WriteString("\n")
		for _, c := range changes {
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
	}

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

	msg := tgbotapi.NewMessage(sub.TelegramChatID, sb.String())
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.DisableWebPagePreview = true

	if _, err := p.tgAPI.Send(msg); err != nil {
		p.log.Error().Err(err).Int64("chat_id", sub.TelegramChatID).Msg("poller: failed to send notification")
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
