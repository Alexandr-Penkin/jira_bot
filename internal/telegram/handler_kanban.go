package telegram

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"golang.org/x/sync/errgroup"

	"SleepJiraBot/internal/format"
	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/locale"
)

const (
	kanbanIssueMax    = 100
	kanbanDefaultDays = 30
	kanbanMaxDays     = 90
	kanbanOldestMax   = 5
	kanbanBoardLimit  = 10
)

var kanbanPeriodChoices = []int{7, 14, kanbanDefaultDays}

// handleKanbanStart asks the user to enter a project key.
func (h *Handler) handleKanbanStart(chatID int64, lang locale.Lang) {
	h.sendPrompt(chatID, locale.T(lang, "kanban.enter_project"), lang)
}

// handleKanbanFull resolves boards for a project and routes to board
// selection (or straight to the report when there is a single board).
// A valid daysArg skips the period picker downstream.
func (h *Handler) handleKanbanFull(ctx context.Context, chatID, userID int64, projectKey, daysArg string) tgbotapi.MessageConfig {
	lang := h.getLang(ctx, userID)

	days := 0
	if daysArg != "" {
		d, err := strconv.Atoi(daysArg)
		if err != nil || d < 1 || d > kanbanMaxDays {
			return tgbotapi.NewMessage(chatID, locale.T(lang, "kanban.invalid_days", kanbanMaxDays))
		}
		days = d
	}

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected"))
	}

	boards, err := h.jiraAPI.GetBoards(ctx, user, projectKey)
	if err != nil {
		h.log.Error().Err(err).Str("project", projectKey).Msg("kanban: failed to get boards")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "sprint.boards_failed"))
	}
	if len(boards) == 0 {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "sprint.no_boards"))
	}
	if len(boards) == 1 {
		return h.handleKanbanBoard(ctx, chatID, userID, boards[0].ID, days)
	}

	if len(boards) > kanbanBoardLimit {
		boards = boards[:kanbanBoardLimit]
	}
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(boards))
	for _, b := range boards {
		data := fmt.Sprintf("kanban_board:%d:%d", b.ID, days)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.Name, data),
		))
	}
	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "sprint.choose_board"))
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	return msg
}

// handleKanbanBoard renders the report when a period is already chosen,
// otherwise shows the period picker for the selected board.
func (h *Handler) handleKanbanBoard(ctx context.Context, chatID, userID int64, boardID, days int) tgbotapi.MessageConfig {
	if days > 0 {
		return h.handleKanbanReport(ctx, chatID, userID, boardID, days)
	}

	lang := h.getLang(ctx, userID)
	row := make([]tgbotapi.InlineKeyboardButton, 0, len(kanbanPeriodChoices))
	for _, d := range kanbanPeriodChoices {
		label := locale.T(lang, "kanban.period_days", d)
		data := fmt.Sprintf("kanban_report:%d:%d", boardID, d)
		row = append(row, tgbotapi.NewInlineKeyboardButtonData(label, data))
	}
	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "kanban.choose_period"))
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(row...))
	return msg
}

// handleKanbanReport fetches the board's completed and in-progress issues
// and renders flow metrics scoped to that board's filter.
func (h *Handler) handleKanbanReport(ctx context.Context, chatID, userID int64, boardID, days int) tgbotapi.MessageConfig {
	lang := h.getLang(ctx, userID)

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected"))
	}

	if days <= 0 {
		days = kanbanDefaultDays
	}

	var doneResult, wipResult *jira.SearchResult
	boardName := ""
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		res, searchErr := h.jiraAPI.SearchBoardIssuesForReport(gctx, user, boardID, kanbanDoneJQL(days, user.DoneStatuses), kanbanIssueMax, user.AssigneeFieldID)
		if searchErr != nil {
			return searchErr
		}
		doneResult = res
		return nil
	})
	g.Go(func() error {
		res, searchErr := h.jiraAPI.SearchBoardIssuesForReport(gctx, user, boardID, kanbanWIPJQL(), kanbanIssueMax, user.AssigneeFieldID)
		if searchErr != nil {
			return searchErr
		}
		wipResult = res
		return nil
	})
	// Board name is cosmetic — never fail the report over it.
	g.Go(func() error {
		if b, boardErr := h.jiraAPI.GetBoard(gctx, user, boardID); boardErr == nil {
			boardName = b.Name
		}
		return nil
	})
	if err = g.Wait(); err != nil {
		h.log.Error().Err(err).Int("board_id", boardID).Int("days", days).Msg("kanban: failed to search issues")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "kanban.report_failed"))
	}

	if len(doneResult.Issues) == 0 && len(wipResult.Issues) == 0 {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "kanban.no_issues"))
	}

	if boardName == "" {
		boardName = fmt.Sprintf("#%d", boardID)
	}

	filterSet := make(map[string]bool, len(user.SprintIssueTypes))
	for _, t := range user.SprintIssueTypes {
		filterSet[t] = true
	}

	now := time.Now()
	m := computeKanbanMetrics(doneResult.Issues, wipResult.Issues, now.AddDate(0, 0, -days), now, filterSet, user.DoneStatuses, user.HoldStatuses, user.AssigneeFieldID != "")
	if m.throughput == 0 && m.wipTotal == 0 {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "kanban.no_issues"))
	}

	text := formatKanbanReport(lang, boardName, days, user.SprintIssueTypes, m)

	parts := splitMessage(text, 4000)
	for i := 0; i < len(parts)-1; i++ {
		partMsg := tgbotapi.NewMessage(chatID, parts[i])
		partMsg.ParseMode = tgbotapi.ModeMarkdown
		h.sendMessage(partMsg)
	}

	msg := tgbotapi.NewMessage(chatID, parts[len(parts)-1])
	msg.ParseMode = tgbotapi.ModeMarkdown
	return msg
}

// handleKanbanCallback routes kanban_board and kanban_report callbacks.
// Callback data format: kanban_board:boardID:days (days 0 = ask) or
// kanban_report:boardID:days.
func (h *Handler) handleKanbanCallback(ctx context.Context, cq *tgbotapi.CallbackQuery, parts []string) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	if len(parts) < 3 {
		return
	}

	boardID, err := strconv.Atoi(parts[1])
	if err != nil {
		return
	}
	days, err := strconv.Atoi(parts[2])
	if err != nil || days < 0 || days > kanbanMaxDays {
		return
	}

	chatID := cq.Message.Chat.ID
	userID := cq.From.ID
	h.states.Clear(chatID, userID)

	switch parts[0] {
	case "kanban_board":
		h.sendMessage(h.handleKanbanBoard(ctx, chatID, userID, boardID, days))
	case "kanban_report":
		if days < 1 {
			return
		}
		h.sendMessage(h.handleKanbanReport(ctx, chatID, userID, boardID, days))
	}
}

// kanbanDoneJQL adds period/category constraints on top of a board's own
// filter. Status NAMES are deliberately kept out of the JQL: a globally
// configured custom done-status may not exist in this board's project, and
// Jira rejects unknown status names with a 400. We therefore filter by
// status category (always valid) and let computeKanbanMetrics classify the
// actual done set from the changelog. The "updated" bound is a safe superset
// — an issue completed within the period always has updated within it.
//
// JQL statusCategory matches on the category NAME ("To Do"/"In Progress"/
// "Done"), not the REST API key ("new"/"indeterminate"/"done").
func kanbanDoneJQL(days int, doneStatuses []string) string {
	scope := `statusCategory = "Done"`
	if len(doneStatuses) > 0 {
		// A custom done-status may live in the "In Progress" category, so
		// widen the net; Go re-checks the real done time per issue.
		scope = `statusCategory in ("In Progress", "Done")`
	}
	return fmt.Sprintf(`%s AND updated >= -%dd ORDER BY updated DESC`, scope, days)
}

// kanbanWIPJQL restricts a board query to in-progress issues. No status
// names (see kanbanDoneJQL); issues sitting in a custom done-status are
// excluded in Go by computeKanbanMetrics.
func kanbanWIPJQL() string {
	return `statusCategory = "In Progress" ORDER BY created ASC`
}

type kanbanFlowStats struct {
	done       int
	cycleSumH  float64
	cycleCount int
}

type kanbanAgedItem struct {
	key  string
	ageH float64
}

type kanbanMetrics struct {
	throughput    int
	weekly        []int // done count per 7-day bucket from period start
	cycleHours    []float64
	leadHours     []float64
	wipByStatus   map[string]int
	wipTotal      int
	ageHours      []float64
	oldest        []kanbanAgedItem
	totalBlockedH float64
	blockedCount  int
	flowEffPct    int // -1 when unknown
	byType        map[string]*kanbanFlowStats
	byAssignee    map[string]*kanbanFlowStats // key "" = unassigned
}

// issueDoneTime returns the timestamp of the last transition into a done
// status, falling back to the issue's updated time when the changelog has
// no such transition (e.g. issue created directly in a done status).
func issueDoneTime(issue *jira.Issue, doneStatuses []string) time.Time {
	var doneTime time.Time
	if issue.Changelog != nil {
		for _, hist := range issue.Changelog.Histories {
			for _, item := range hist.Items {
				if !strings.EqualFold(item.Field, "status") {
					continue
				}
				if !isDoneStatus(strings.ToLower(item.ToString), doneStatuses) {
					continue
				}
				ts, err := parseJiraTime(hist.Created)
				if err != nil {
					continue
				}
				if ts.After(doneTime) {
					doneTime = ts
				}
			}
		}
	}
	if doneTime.IsZero() && issue.Fields.Updated != "" {
		if ts, err := parseJiraTime(issue.Fields.Updated); err == nil {
			doneTime = ts
		}
	}
	return doneTime
}

// firstInProgressTime returns the timestamp of the first transition into an
// in-progress status, or zero when none is recorded.
func firstInProgressTime(issue *jira.Issue) time.Time {
	var first time.Time
	if issue.Changelog == nil {
		return first
	}
	for _, hist := range issue.Changelog.Histories {
		for _, item := range hist.Items {
			if !strings.EqualFold(item.Field, "status") {
				continue
			}
			if !isInProgressStatus(strings.ToLower(item.ToString)) {
				continue
			}
			ts, err := parseJiraTime(hist.Created)
			if err != nil {
				continue
			}
			if first.IsZero() || ts.Before(first) {
				first = ts
			}
		}
	}
	return first
}

// sumHoldHours sums the time an issue spent in hold statuses; an interval
// still open is closed at `until`.
func sumHoldHours(histories []jira.ChangeHistory, holdStatuses []string, until time.Time) float64 {
	var blockedStart time.Time
	var total float64
	for _, hist := range histories {
		for _, item := range hist.Items {
			if !strings.EqualFold(item.Field, "status") {
				continue
			}
			ts, err := parseJiraTime(hist.Created)
			if err != nil {
				continue
			}
			if isHoldStatus(strings.ToLower(item.ToString), holdStatuses) && blockedStart.IsZero() {
				blockedStart = ts
			}
			if isHoldStatus(strings.ToLower(item.FromString), holdStatuses) && !blockedStart.IsZero() {
				total += ts.Sub(blockedStart).Hours()
				blockedStart = time.Time{}
			}
		}
	}
	if !blockedStart.IsZero() && until.After(blockedStart) {
		total += until.Sub(blockedStart).Hours()
	}
	return total
}

// issueAssigneeName returns the effective assignee display name ("" = unassigned).
func issueAssigneeName(f *jira.IssueFields, useCustomAssignee bool) string {
	if useCustomAssignee {
		if f.CustomAssignee != nil {
			return f.CustomAssignee.DisplayName
		}
		return ""
	}
	if f.Assignee != nil {
		return f.Assignee.DisplayName
	}
	return ""
}

// computeKanbanMetrics derives flow metrics from issues completed within the
// period and issues currently in progress.
func computeKanbanMetrics(doneIssues, wipIssues []jira.Issue, periodStart, now time.Time, filterSet map[string]bool, doneStatuses, holdStatuses []string, useCustomAssignee bool) *kanbanMetrics {
	isFiltered := len(filterSet) > 0
	weekCount := int(math.Ceil(now.Sub(periodStart).Hours() / (24 * 7)))
	if weekCount < 1 {
		weekCount = 1
	}
	m := &kanbanMetrics{
		weekly:      make([]int, weekCount),
		wipByStatus: make(map[string]int),
		byType:      make(map[string]*kanbanFlowStats),
		byAssignee:  make(map[string]*kanbanFlowStats),
	}

	var cycleSumH, cycleBlockedSumH float64

	for i := range doneIssues {
		issue := &doneIssues[i]
		typeName := "Other"
		if issue.Fields.IssueType != nil {
			typeName = issue.Fields.IssueType.Name
		}
		if isFiltered && !filterSet[typeName] {
			continue
		}

		// The done query may include "In Progress"-category issues (to catch
		// custom done-statuses living in that category). Count only issues
		// that are actually done now, so issueDoneTime's updated fallback
		// never misfires on a still-open issue.
		if statusCategory(issue, doneStatuses, holdStatuses) != "done" {
			continue
		}

		doneTime := issueDoneTime(issue, doneStatuses)
		if doneTime.IsZero() || doneTime.Before(periodStart) || doneTime.After(now) {
			continue
		}

		m.throughput++
		week := int(doneTime.Sub(periodStart).Hours() / (24 * 7))
		if week >= weekCount {
			week = weekCount - 1
		}
		m.weekly[week]++

		ts := m.byType[typeName]
		if ts == nil {
			ts = &kanbanFlowStats{}
			m.byType[typeName] = ts
		}
		as := m.byAssignee[issueAssigneeName(&issue.Fields, useCustomAssignee)]
		if as == nil {
			as = &kanbanFlowStats{}
			m.byAssignee[issueAssigneeName(&issue.Fields, useCustomAssignee)] = as
		}
		ts.done++
		as.done++

		if started := firstInProgressTime(issue); !started.IsZero() && doneTime.After(started) {
			cycleH := doneTime.Sub(started).Hours()
			m.cycleHours = append(m.cycleHours, cycleH)
			cycleSumH += cycleH
			ts.cycleSumH += cycleH
			ts.cycleCount++
			as.cycleSumH += cycleH
			as.cycleCount++

			if issue.Changelog != nil {
				blockedH := sumHoldHours(issue.Changelog.Histories, holdStatuses, doneTime)
				if blockedH > cycleH {
					blockedH = cycleH
				}
				cycleBlockedSumH += blockedH
			}
		}

		if issue.Fields.Created != "" {
			if created, err := parseJiraTime(issue.Fields.Created); err == nil && doneTime.After(created) {
				m.leadHours = append(m.leadHours, doneTime.Sub(created).Hours())
			}
		}

		if issue.Changelog != nil {
			if blockedH := sumHoldHours(issue.Changelog.Histories, holdStatuses, doneTime); blockedH > 0 {
				m.totalBlockedH += blockedH
				m.blockedCount++
			}
		}
	}

	m.flowEffPct = -1
	if cycleSumH > 0 {
		eff := (cycleSumH - cycleBlockedSumH) / cycleSumH * 100
		if eff < 0 {
			eff = 0
		}
		m.flowEffPct = int(math.Round(eff))
	}

	for i := range wipIssues {
		issue := &wipIssues[i]
		typeName := "Other"
		if issue.Fields.IssueType != nil {
			typeName = issue.Fields.IssueType.Name
		}
		if isFiltered && !filterSet[typeName] {
			continue
		}
		if statusCategory(issue, doneStatuses, holdStatuses) == "done" {
			continue
		}

		statusName := "Other"
		if issue.Fields.Status != nil {
			statusName = issue.Fields.Status.Name
		}
		m.wipByStatus[statusName]++
		m.wipTotal++

		started := firstInProgressTime(issue)
		if started.IsZero() && issue.Fields.Created != "" {
			started, _ = parseJiraTime(issue.Fields.Created)
		}
		if !started.IsZero() && now.After(started) {
			ageH := now.Sub(started).Hours()
			m.ageHours = append(m.ageHours, ageH)
			m.oldest = append(m.oldest, kanbanAgedItem{key: issue.Key, ageH: ageH})
		}

		if issue.Changelog != nil {
			if blockedH := sumHoldHours(issue.Changelog.Histories, holdStatuses, now); blockedH > 0 {
				m.totalBlockedH += blockedH
				m.blockedCount++
			}
		}
	}

	sort.Slice(m.oldest, func(i, j int) bool {
		return m.oldest[i].ageH > m.oldest[j].ageH
	})
	if len(m.oldest) > kanbanOldestMax {
		m.oldest = m.oldest[:kanbanOldestMax]
	}

	return m
}

// percentile returns the p-th percentile (0..100, nearest-rank) of values.
func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)
	idx := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func formatKanbanReport(lang locale.Lang, boardName string, days int, issueTypeFilter []string, m *kanbanMetrics) string {
	var sb strings.Builder

	// Header.
	sb.WriteString("📊 *")
	sb.WriteString(locale.T(lang, "kanban.report_title"))
	sb.WriteString("*: ")
	sb.WriteString(format.EscapeMarkdown(boardName))
	sb.WriteString("\n🗓 _")
	sb.WriteString(locale.T(lang, "kanban.period", days))
	sb.WriteString("_\n")
	if len(issueTypeFilter) > 0 {
		sb.WriteString("🏷 _")
		sb.WriteString(locale.T(lang, "sprint.filtered", strings.Join(issueTypeFilter, ", ")))
		sb.WriteString("_\n")
	}
	sb.WriteString("\n")

	// Throughput.
	fmt.Fprintf(&sb, "🚚 *%s:* ", locale.T(lang, "kanban.throughput"))
	sb.WriteString(locale.T(lang, "kanban.issues_n", m.throughput))
	if m.throughput > 0 && len(m.weekly) > 0 {
		fmt.Fprintf(&sb, " (%s)", locale.T(lang, "kanban.per_week", float64(m.throughput)/float64(len(m.weekly))))
		weeks := make([]string, len(m.weekly))
		for i, c := range m.weekly {
			weeks[i] = strconv.Itoa(c)
		}
		sb.WriteString("\n  ")
		sb.WriteString(locale.T(lang, "kanban.weekly", strings.Join(weeks, " · ")))
	}
	sb.WriteString("\n")

	// Cycle / lead time.
	if len(m.cycleHours) > 0 {
		fmt.Fprintf(&sb, "\n⏱ *%s:* ", locale.T(lang, "kanban.cycle_time"))
		sb.WriteString(locale.T(lang, "kanban.cycle_detail",
			formatDuration(average(m.cycleHours)),
			formatDuration(percentile(m.cycleHours, 50)),
			formatDuration(percentile(m.cycleHours, 85)),
			len(m.cycleHours)))
		sb.WriteString("\n")
	}
	if len(m.leadHours) > 0 {
		fmt.Fprintf(&sb, "📦 *%s:* ", locale.T(lang, "kanban.lead_time"))
		sb.WriteString(locale.T(lang, "kanban.lead_detail",
			formatDuration(average(m.leadHours)),
			formatDuration(percentile(m.leadHours, 50))))
		sb.WriteString("\n")
	}

	// WIP by status.
	fmt.Fprintf(&sb, "\n🔄 *%s:* %d\n", locale.T(lang, "kanban.wip"), m.wipTotal)
	statusKeys := make([]string, 0, len(m.wipByStatus))
	for k := range m.wipByStatus {
		statusKeys = append(statusKeys, k)
	}
	sort.Slice(statusKeys, func(i, j int) bool {
		if m.wipByStatus[statusKeys[i]] != m.wipByStatus[statusKeys[j]] {
			return m.wipByStatus[statusKeys[i]] > m.wipByStatus[statusKeys[j]]
		}
		return statusKeys[i] < statusKeys[j]
	})
	for _, name := range statusKeys {
		fmt.Fprintf(&sb, "• %s: %d\n", name, m.wipByStatus[name])
	}

	// Work item age.
	if len(m.ageHours) > 0 {
		fmt.Fprintf(&sb, "\n⌛ *%s:* ", locale.T(lang, "kanban.item_age"))
		sb.WriteString(locale.T(lang, "kanban.age_detail",
			formatDuration(average(m.ageHours)),
			formatDuration(percentile(m.ageHours, 100))))
		sb.WriteString("\n")
		if len(m.oldest) > 0 {
			sb.WriteString("  ")
			sb.WriteString(locale.T(lang, "kanban.oldest"))
			sb.WriteString(": ")
			items := make([]string, 0, len(m.oldest))
			for _, it := range m.oldest {
				items = append(items, fmt.Sprintf("`%s` (%s)", it.key, formatDuration(it.ageH)))
			}
			sb.WriteString(strings.Join(items, ", "))
			sb.WriteString("\n")
		}
	}

	// Blocked time / flow efficiency.
	if m.blockedCount > 0 {
		fmt.Fprintf(&sb, "\n⏸ *%s:* ", locale.T(lang, "kanban.blocked_time"))
		sb.WriteString(locale.T(lang, "sprint.blocked_detail",
			formatDuration(m.totalBlockedH),
			formatDuration(m.totalBlockedH/float64(m.blockedCount)),
			m.blockedCount))
		sb.WriteString("\n")
	}
	if m.flowEffPct >= 0 {
		fmt.Fprintf(&sb, "🌊 *%s:* %d%%\n", locale.T(lang, "kanban.flow_efficiency"), m.flowEffPct)
	}

	writeKanbanBreakdown(&sb, lang, "📝", locale.T(lang, "kanban.by_type"), m.byType, "")
	writeKanbanBreakdown(&sb, lang, "👥", locale.T(lang, "kanban.by_assignee"), m.byAssignee, locale.T(lang, "sprint.unassigned"))

	return sb.String()
}

// writeKanbanBreakdown renders a done-count + avg-cycle section for a
// type or assignee map. emptyKeyLabel substitutes the "" key (unassigned).
func writeKanbanBreakdown(sb *strings.Builder, lang locale.Lang, icon, title string, stats map[string]*kanbanFlowStats, emptyKeyLabel string) {
	if len(stats) == 0 {
		return
	}
	keys := make([]string, 0, len(stats))
	for k := range stats {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if stats[keys[i]].done != stats[keys[j]].done {
			return stats[keys[i]].done > stats[keys[j]].done
		}
		return keys[i] < keys[j]
	})

	fmt.Fprintf(sb, "\n%s *%s*\n", icon, title)
	for _, k := range keys {
		s := stats[k]
		name := k
		if name == "" {
			name = emptyKeyLabel
		}
		fmt.Fprintf(sb, "• %s: %d ✅", name, s.done)
		if s.cycleCount > 0 {
			fmt.Fprintf(sb, " · %s", locale.T(lang, "kanban.cycle_avg", formatDuration(s.cycleSumH/float64(s.cycleCount))))
		}
		sb.WriteString("\n")
	}
}
