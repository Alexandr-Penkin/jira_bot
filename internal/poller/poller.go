package poller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/rs/zerolog"

	"SleepJiraBot/internal/format"
	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/locale"
	"SleepJiraBot/internal/notifydedup"
	"SleepJiraBot/internal/storage"
	eventsv1 "SleepJiraBot/pkg/events/v1"
)

const (
	defaultPollInterval   = 30 * time.Second
	defaultBatchWindow    = 1 * time.Minute
	pollTimeout           = 30 * time.Second
	pollMaxResults        = 50
	mentionCommentPage    = 50
	mentionCommentMaxPage = 5 // hard cap: 5 * 50 = 250 comments scanned per issue per poll
	mentionCommentTimeout = 10 * time.Second
)

// mergedChange tracks a field change, collapsing intermediate states
// (e.g. Status: Open → In Progress → Done becomes Status: Open → Done).
type mergedChange struct {
	Field      string
	From       string
	FromString string
	To         string
	ToString   string
}

// changeDisplayValue returns the human-readable value for one side of a
// change item, falling back to the raw ID when the *String form is empty
// (typical for some custom field types like user pickers or sprints).
func changeDisplayValue(str, raw string) string {
	if str != "" {
		return str
	}
	return raw
}

// changeFieldName returns the display name for a changelog field, falling
// back to the field ID when the friendly name is missing (rare but happens
// for some custom fields).
func changeFieldName(field, fieldID string) string {
	if field != "" {
		return field
	}
	return fieldID
}

// pendingNotification accumulates changes for a single issue
// destined for a specific chat before sending one merged notification.
type pendingNotification struct {
	chatID    int64
	issueKey  string
	siteURL   string
	issue     *jira.Issue
	lang      locale.Lang
	authors   map[string]string        // accountID -> displayName
	changes   map[string]*mergedChange // field -> merged change
	firstSeen time.Time
	// isMention is true when this notification was triggered by a comment
	// mention rather than by changelog entries. Mention notifications are
	// allowed to have an empty changes section; everything else must have
	// at least one real change to be sent.
	isMention bool
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
	mu          sync.RWMutex
	lastPollAt  time.Time
	pub         eventsv1.Publisher
}

func New(subRepo *storage.SubscriptionRepo, userRepo *storage.UserRepo, jiraAPI *jira.Client, tgAPI *tgbotapi.BotAPI, log zerolog.Logger, interval, batchWindow time.Duration, dedup *notifydedup.Guard) *Poller {
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
		pub:         eventsv1.NoopPublisher{},
	}
}

// SetEventPublisher installs a domain event publisher. ChangeDetected
// events are emitted per (subscription, issue) detection window.
func (p *Poller) SetEventPublisher(pub eventsv1.Publisher) {
	if pub == nil {
		p.pub = eventsv1.NoopPublisher{}
		return
	}
	p.pub = pub
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
	p.mu.RLock()
	pendingCount := len(p.pending)
	lastPoll := p.lastPollAt
	p.mu.RUnlock()
	return PollerStatus{
		Interval:     p.interval,
		BatchWindow:  p.batchWindow,
		PendingCount: pendingCount,
		LastPollAt:   lastPoll,
	}
}

func (p *Poller) poll(ctx context.Context) {
	p.mu.Lock()
	p.lastPollAt = time.Now()
	p.mu.Unlock()

	// Get distinct user IDs from active subscriptions.
	userIDs, err := p.subRepo.GetActiveUserIDs(ctx)
	if err != nil {
		p.log.Error().Err(err).Msg("poller: failed to get subscription user IDs")
		return
	}

	// Info-level heartbeat so docker logs show the poller is alive even
	// during quiet cycles when no changes are detected.
	p.log.Info().
		Int("users", len(userIDs)).
		Msg("poller: cycle start")

	// Cap each user's work at the poll interval so a single slow Jira
	// site can't starve the rest of the users for many minutes.
	userBudget := p.interval
	if userBudget < 30*time.Second {
		userBudget = 30 * time.Second
	}

	for _, userID := range userIDs {
		if ctx.Err() != nil {
			return
		}
		subs, err := p.subRepo.GetActiveByUser(ctx, userID)
		if err != nil {
			p.log.Error().Err(err).Int64("user_id", userID).Msg("poller: failed to get user subscriptions")
			continue
		}
		userCtx, cancel := context.WithTimeout(ctx, userBudget)
		p.pollUser(userCtx, userID, subs)
		cancel()
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

	// Backfill the Jira display name for users who connected before the
	// my_mentions `text ~` widening shipped. Cheap no-op for records that
	// already have it — guarded so we don't re-hit /myself on every poll.
	if user.JiraDisplayName == "" && user.JiraAccountID != "" {
		p.backfillDisplayName(ctx, user)
	}

	for i := range subs {
		sub := &subs[i]
		jql := p.buildJQL(sub, user)
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
			if errors.Is(err, jira.ErrTokenInvalid) {
				p.handleInvalidToken(ctx, user)
				return
			}
			p.log.Error().Err(err).
				Str("sub_type", sub.SubscriptionType).
				Int64("user_id", telegramUserID).
				Msg("poller: failed to search issues")
			continue
		}

		// Per-subscription Info log so the operator can tell whether
		// Jira returned anything for each query, separating "fetch
		// returned 0" from "fetched N but all filtered out later".
		if len(result.Issues) > 0 {
			p.log.Info().
				Int64("user_id", telegramUserID).
				Str("sub_type", sub.SubscriptionType).
				Int("issues", len(result.Issues)).
				Str("since", sinceStr).
				Msg("poller: jira returned issues")
		}

		// Per-stage drop counters so the operator can see where issues
		// disappear between Jira's response and the outbound notification.
		var (
			droppedStale    int
			droppedNoMent   int
			droppedDupChat  int
			passedToPending int
		)

		sinceTime := time.Unix(sinceTS, 0)
		for j := range result.Issues {
			issue := &result.Issues[j]

			// JQL has minute precision so it returns issues from the
			// truncated window. Filter precisely using the issue's Updated
			// timestamp to skip issues already processed in prior cycles.
			if issue.Fields.Updated != "" {
				updatedAt, parseErr := time.Parse("2006-01-02T15:04:05.000-0700", issue.Fields.Updated)
				if parseErr == nil && !updatedAt.After(sinceTime) {
					droppedStale++
					continue
				}
			}

			// For mention subscriptions, only notify if user is actually mentioned in recent comments.
			if sub.SubscriptionType == storage.SubTypeMyMentions {
				if !p.isUserMentionedInComments(ctx, user, issue.Key, sinceTS) {
					droppedNoMent++
					continue
				}
			}

			isMention := sub.SubscriptionType == storage.SubTypeMyMentions

			// If already accumulating changes for this issue+chat, just merge.
			pendingKey := fmt.Sprintf("%d:%s", sub.TelegramChatID, issue.Key)
			if _, inPending := p.pending[pendingKey]; inPending {
				p.addPending(sub.TelegramChatID, issue, user.JiraSiteURL, sinceTS, user.JiraAccountID, locale.FromString(user.Language), isMention)
				passedToPending++
				p.emitChangeDetected(ctx, sub, issue)
				continue
			}

			if notified[sub.TelegramChatID] == nil {
				notified[sub.TelegramChatID] = make(map[string]bool)
			}
			if notified[sub.TelegramChatID][issue.Key] {
				droppedDupChat++
				continue
			}
			notified[sub.TelegramChatID][issue.Key] = true

			p.addPending(sub.TelegramChatID, issue, user.JiraSiteURL, sinceTS, user.JiraAccountID, locale.FromString(user.Language), isMention)
			passedToPending++
			p.emitChangeDetected(ctx, sub, issue)
		}

		if len(result.Issues) > 0 {
			p.log.Info().
				Int64("user_id", telegramUserID).
				Str("sub_type", sub.SubscriptionType).
				Int("fetched", len(result.Issues)).
				Int("dropped_stale", droppedStale).
				Int("dropped_no_mention", droppedNoMent).
				Int("dropped_dup_chat", droppedDupChat).
				Int("passed_to_addPending", passedToPending).
				Msg("poller: filter summary")
		}

		if err := p.subRepo.UpdateLastPolled(ctx, sub.ID, now); err != nil {
			p.log.Error().Err(err).Msg("poller: failed to update last_polled_at")
		}
	}
}

func (p *Poller) buildJQL(sub *storage.Subscription, user *storage.User) string {
	switch sub.SubscriptionType {
	case storage.SubTypeMyNewIssues:
		return "assignee = currentUser()"
	case storage.SubTypeMyMentions:
		// Jira has no direct "mentioned me" JQL clause. We pre-filter
		// with two OR'd strategies:
		//   1. Any issue the user is already related to (watcher,
		//      assignee, reporter, voter, worklog author) — catches
		//      "warm" mentions cheaply via user-relation clauses.
		//   2. Full-text search for the user's display name — catches
		//      cold mentions in unrelated issues. Jira's text index
		//      covers comment bodies, and mention nodes are indexed
		//      as "@Display Name", so `text ~ "\"<name>\""` returns
		//      them. False positives (someone typed the name without
		//      actually @-mentioning) are filtered downstream by
		//      isUserMentionedInComments, which verifies the mention
		//      via account ID in the ADF tree.
		relation := "watcher = currentUser() OR assignee = currentUser() OR reporter = currentUser() OR voter = currentUser() OR worklogAuthor = currentUser()"
		if user != nil && user.JiraDisplayName != "" {
			return fmt.Sprintf("(%s OR text ~ %q)", relation, `"`+user.JiraDisplayName+`"`)
		}
		return "(" + relation + ")"
	case storage.SubTypeMyWatched:
		return "watcher = currentUser()"
	case storage.SubTypeProjectUpdates:
		if sub.JiraProjectKey == "" {
			return ""
		}
		return fmt.Sprintf("project = %q", sub.JiraProjectKey)
	case storage.SubTypeIssueUpdates:
		if sub.JiraIssueKey == "" {
			return ""
		}
		return fmt.Sprintf("key = %q", sub.JiraIssueKey)
	case storage.SubTypeFilterUpdates:
		if sub.JiraFilterJQL == "" {
			return ""
		}
		// User-supplied JQL may contain a trailing ORDER BY (which we
		// will re-add ourselves) and top-level OR clauses. Strip ORDER
		// BY and wrap in parentheses so the caller can safely AND on
		// additional predicates without changing operator precedence.
		cleaned := stripOrderBy(sub.JiraFilterJQL)
		if cleaned == "" {
			return ""
		}
		return "(" + cleaned + ")"
	default:
		return ""
	}
}

// stripOrderBy removes a top-level "ORDER BY ..." clause from a JQL
// string. Returns the trimmed remainder so it can be safely composed with
// other predicates.
func stripOrderBy(jql string) string {
	jql = strings.TrimSpace(jql)
	upper := strings.ToUpper(jql)
	if idx := strings.LastIndex(upper, " ORDER BY "); idx != -1 {
		return strings.TrimSpace(jql[:idx])
	}
	if strings.HasPrefix(upper, "ORDER BY ") {
		return ""
	}
	return jql
}

func (p *Poller) sinceTimestamp(sub *storage.Subscription) int64 {
	fallback := time.Now().Add(-p.interval).Unix()
	if sub.LastPolledAt > 0 {
		return sub.LastPolledAt
	}
	return fallback
}

// emitChangeDetected publishes one sjb.subscription.change_detected.v1 per
// (subscription, issue) match. It is best-effort and never blocks the
// polling loop.
func (p *Poller) emitChangeDetected(ctx context.Context, sub *storage.Subscription, issue *jira.Issue) {
	changeType := "updated"
	if issue.Fields.Status != nil && strings.EqualFold(issue.Fields.Status.StatusCategory.Key, "new") {
		changeType = "created"
	}
	_ = p.pub.Publish(ctx, eventsv1.ChangeDetected{
		SubscriptionID:   sub.ID.Hex(),
		SubscriptionType: sub.SubscriptionType,
		TelegramID:       sub.TelegramUserID,
		ChatID:           sub.TelegramChatID,
		IssueKey:         issue.Key,
		ChangeType:       changeType,
		DetectedAt:       time.Now().Unix(),
	}, "")
}

func (p *Poller) addPending(chatID int64, issue *jira.Issue, siteURL string, sinceTS int64, excludeAccountID string, lang locale.Lang, isMention bool) {
	key := fmt.Sprintf("%d:%s", chatID, issue.Key)

	authors, changes := recentChanges(issue, sinceTS, excludeAccountID)

	// Skip if there is nothing to report. Mention notifications are
	// allowed through even when changelog is empty in this window —
	// the trigger for them is the comment mention itself.
	if len(changes) == 0 && !isMention {
		p.log.Info().
			Int64("chat_id", chatID).
			Str("issue", issue.Key).
			Msg("poller: dropped in addPending — no changes in window")
		return
	}
	// Non-mention notification with no recent changes by anyone other than
	// the current user — also skip (would produce an empty notification).
	if len(authors) == 0 && len(changes) == 0 {
		p.log.Info().
			Int64("chat_id", chatID).
			Str("issue", issue.Key).
			Msg("poller: dropped in addPending — no external authors")
		return
	}

	pn, exists := p.pending[key]
	if !exists {
		pn = &pendingNotification{
			chatID:    chatID,
			issueKey:  issue.Key,
			siteURL:   siteURL,
			issue:     issue,
			lang:      lang,
			authors:   make(map[string]string),
			changes:   make(map[string]*mergedChange),
			firstSeen: time.Now(),
			isMention: isMention,
		}
		p.pending[key] = pn
	} else if isMention {
		pn.isMention = true
	}

	// Always keep the latest issue state.
	pn.issue = issue

	for _, author := range authors {
		pn.authors[author.AccountID] = author.DisplayName
	}

	// Merge changes: keep the original "from" side, update the "to" side.
	for _, c := range changes {
		key := changeFieldName(c.Field, c.FieldID)
		if key == "" {
			continue
		}
		if existing, ok := pn.changes[key]; ok {
			existing.To = c.To
			existing.ToString = c.ToString
		} else {
			pn.changes[key] = &mergedChange{
				Field:      key,
				From:       c.From,
				FromString: c.FromString,
				To:         c.To,
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
	lang := pn.lang

	// Build the changes section first so we can bail out when there is
	// nothing meaningful to report (and avoid sending an empty
	// "X updated MAIN-XXX" with no body).
	var changesSB strings.Builder
	hasChanges := false
	for _, c := range pn.changes {
		fromVal := changeDisplayValue(c.FromString, c.From)
		toVal := changeDisplayValue(c.ToString, c.To)
		if fromVal == toVal {
			continue
		}
		if !hasChanges {
			changesSB.WriteString("\n")
			hasChanges = true
		}
		switch {
		case fromVal != "" && toVal != "":
			fmt.Fprintf(&changesSB, "%s: %s → %s\n",
				format.EscapeMarkdown(c.Field),
				format.EscapeMarkdown(fromVal),
				format.EscapeMarkdown(toVal))
		case toVal != "":
			fmt.Fprintf(&changesSB, "%s: %s\n",
				format.EscapeMarkdown(c.Field),
				format.EscapeMarkdown(toVal))
		default:
			fmt.Fprintf(&changesSB, "%s: %s → %s\n",
				format.EscapeMarkdown(c.Field),
				format.EscapeMarkdown(fromVal),
				format.EscapeMarkdown(locale.T(lang, "notif.cleared")))
		}
	}

	if !hasChanges && !pn.isMention {
		p.log.Debug().
			Int64("chat_id", pn.chatID).
			Str("issue", pn.issueKey).
			Msg("poller: skipping notification with no changes")
		return
	}

	// Consume the dedup slot only now that we are committed to sending.
	// Doing this earlier (at poll time) would block follow-up polls for
	// ~ttl even when the notification gets filtered out as empty, which
	// silently dropped real updates for minutes at a time.
	if !p.dedup.Allow(pn.chatID, pn.issueKey) {
		p.log.Debug().
			Int64("chat_id", pn.chatID).
			Str("issue", pn.issueKey).
			Msg("poller: skipping duplicate notification")
		return
	}

	var sb strings.Builder

	// Collect author names.
	var authorNames []string
	for _, name := range pn.authors {
		authorNames = append(authorNames, name)
	}
	authorStr := locale.T(lang, "notif.someone")
	if len(authorNames) > 0 {
		authorStr = strings.Join(authorNames, ", ")
	}
	fmt.Fprintf(&sb, "%s\n", locale.T(lang, "notif.updates",
		format.EscapeMarkdown(authorStr), pn.issueKey, issueURL))

	sb.WriteString(changesSB.String())

	issue := pn.issue
	sb.WriteString("\n")
	fmt.Fprintf(&sb, "%s: %s\n", locale.T(lang, "notif.summary"), format.EscapeMarkdown(issue.Fields.Summary))
	if issue.Fields.Assignee != nil {
		fmt.Fprintf(&sb, "%s: %s\n", locale.T(lang, "notif.assignee"), format.EscapeMarkdown(issue.Fields.Assignee.DisplayName))
	} else {
		fmt.Fprintf(&sb, "%s: %s\n", locale.T(lang, "notif.assignee"), locale.T(lang, "notif.unassigned"))
	}
	if issue.Fields.Reporter != nil {
		fmt.Fprintf(&sb, "%s: %s\n", locale.T(lang, "notif.reporter"), format.EscapeMarkdown(issue.Fields.Reporter.DisplayName))
	}
	if issue.Fields.Priority != nil {
		fmt.Fprintf(&sb, "%s: %s\n", locale.T(lang, "notif.priority"), format.EscapeMarkdown(issue.Fields.Priority.Name))
	}
	if issue.Fields.IssueType != nil {
		fmt.Fprintf(&sb, "%s: %s\n", locale.T(lang, "notif.issue_type"), format.EscapeMarkdown(issue.Fields.IssueType.Name))
	}
	if issue.Fields.Status != nil {
		fmt.Fprintf(&sb, "%s: %s\n", locale.T(lang, "notif.status"), format.EscapeMarkdown(issue.Fields.Status.Name))
	}

	msg := tgbotapi.NewMessage(pn.chatID, sb.String())
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.DisableWebPagePreview = true

	if _, err := p.tgAPI.Send(msg); err != nil {
		p.log.Error().Err(err).Int64("chat_id", pn.chatID).Msg("poller: failed to send notification")
		return
	}
}

// handleInvalidToken is called when Jira rejects a refresh token as permanently
// invalid. It clears the user's credentials and sends a notification asking
// them to reconnect.
func (p *Poller) handleInvalidToken(ctx context.Context, user *storage.User) {
	p.log.Warn().Int64("user_id", user.TelegramUserID).Msg("poller: refresh token invalid, auto-disconnecting user")

	if err := p.userRepo.ClearJiraCredentials(ctx, user.TelegramUserID); err != nil {
		p.log.Error().Err(err).Int64("user_id", user.TelegramUserID).Msg("poller: failed to clear credentials on token invalidation")
	}

	lang := locale.FromString(user.Language)
	msg := tgbotapi.NewMessage(user.TelegramUserID, locale.T(lang, "disconnect.token_expired"))
	if _, err := p.tgAPI.Send(msg); err != nil {
		p.log.Error().Err(err).Int64("user_id", user.TelegramUserID).Msg("poller: failed to notify user about token invalidation")
	}
}

// backfillDisplayName fetches the user's Jira display name via /myself and
// persists it. Used on poll start for users who connected before the
// display name was stored — mutates the in-memory `user` so the current
// poll cycle benefits from the wider JQL immediately.
func (p *Poller) backfillDisplayName(ctx context.Context, user *storage.User) {
	fetchCtx, cancel := context.WithTimeout(ctx, mentionCommentTimeout)
	defer cancel()

	myself, err := p.jiraAPI.GetMyself(fetchCtx, user)
	if err != nil || myself == nil || myself.DisplayName == "" {
		if err != nil {
			p.log.Debug().Err(err).Int64("user_id", user.TelegramUserID).Msg("poller: failed to backfill display name")
		}
		return
	}
	if err := p.userRepo.SetJiraIdentity(ctx, user.TelegramUserID, user.JiraAccountID, myself.DisplayName); err != nil {
		p.log.Warn().Err(err).Int64("user_id", user.TelegramUserID).Msg("poller: failed to persist backfilled display name")
		return
	}
	user.JiraDisplayName = myself.DisplayName
}

// commentActivityTime returns the most recent activity timestamp of a
// comment — max(created, updated) — so edits that introduce a mention are
// treated as fresh events even when the original comment is old.
func commentActivityTime(comment *jira.Comment) time.Time {
	const layout = "2006-01-02T15:04:05.000-0700"
	var newest time.Time
	if t, err := time.Parse(layout, comment.Created); err == nil {
		newest = t
	}
	if comment.Updated != "" {
		if t, err := time.Parse(layout, comment.Updated); err == nil && t.After(newest) {
			newest = t
		}
	}
	return newest
}

// isUserMentionedInComments checks if the user's Jira account ID appears
// in recent comments of the given issue.
func (p *Poller) isUserMentionedInComments(ctx context.Context, user *storage.User, issueKey string, sinceTS int64) bool {
	if user.JiraAccountID == "" {
		return false
	}

	commentCtx, cancel := context.WithTimeout(ctx, mentionCommentTimeout)
	defer cancel()

	sinceTime := time.Unix(sinceTS, 0)

	// Paginate through comments (newest first). Jira doesn't support
	// ordering by `updated`, so we keep scanning until every comment on
	// a full page is older than sinceTime by BOTH created and updated —
	// that guarantees we've seen any comment, including ones that were
	// edited in-window, without fetching the entire history.
	for page := 0; page < mentionCommentMaxPage; page++ {
		resp, err := p.jiraAPI.GetIssueCommentsPage(commentCtx, user, issueKey, page*mentionCommentPage, mentionCommentPage, "-created")
		if err != nil {
			p.log.Debug().Err(err).Str("issue", issueKey).Msg("poller: failed to get comments for mention check")
			return false
		}
		if len(resp.Comments) == 0 {
			return false
		}

		anyInWindow := false
		for i := range resp.Comments {
			comment := &resp.Comments[i]

			ts := commentActivityTime(comment)
			if ts.IsZero() || ts.Before(sinceTime) {
				continue
			}
			anyInWindow = true

			// Check ADF body for mention nodes with user's account ID.
			for _, id := range comment.Body.ExtractMentionIDs() {
				if id == user.JiraAccountID {
					return true
				}
			}
		}

		// No comment on this page touched the window — older pages
		// cannot either, since they contain strictly older `created`
		// timestamps and any fresh `updated` would have landed here.
		if !anyInWindow {
			return false
		}
		if resp.StartAt+len(resp.Comments) >= resp.Total {
			return false
		}
	}

	return false
}

// recentChanges extracts changelog entries created strictly after sinceTS,
// skipping entries authored by excludeAccountID. Returns (nil, nil) when
// the changelog has no matching entries — callers must NOT fabricate an
// author from older history, otherwise they would emit an empty/incorrect
// notification when Jira bumps the issue's `updated` timestamp without a
// corresponding recent changelog entry.
//
// All distinct authors in the window are returned so a merged
// notification can credit every person who touched the issue, not just
// the first one.
func recentChanges(issue *jira.Issue, sinceTS int64, excludeAccountID string) ([]*jira.JiraUser, []jira.ChangeItem) {
	if issue.Changelog == nil {
		return nil, nil
	}

	sinceTime := time.Unix(sinceTS, 0)
	var authors []*jira.JiraUser
	seen := make(map[string]bool)
	var items []jira.ChangeItem

	for _, h := range issue.Changelog.Histories {
		created, err := time.Parse("2006-01-02T15:04:05.000-0700", h.Created)
		if err != nil {
			continue
		}
		if !created.After(sinceTime) {
			continue
		}
		// Skip changes made by the current user.
		if excludeAccountID != "" && h.Author != nil && h.Author.AccountID == excludeAccountID {
			continue
		}
		if h.Author != nil && !seen[h.Author.AccountID] {
			seen[h.Author.AccountID] = true
			authors = append(authors, h.Author)
		}
		items = append(items, h.Items...)
	}

	return authors, items
}
