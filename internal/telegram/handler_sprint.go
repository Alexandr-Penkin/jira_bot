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
	"SleepJiraBot/internal/storage"
)

const (
	sprintIssueMax  = 100
	sprintListLimit = 10
)

// handleSprintStart asks the user to enter a project key.
func (h *Handler) handleSprintStart(chatID int64, lang locale.Lang) {
	h.sendPrompt(chatID, locale.T(lang, "sprint.enter_project"), lang)
}

// handleSprintFull finds boards for a project and optionally matches by board and sprint hints (name or ID).
func (h *Handler) handleSprintFull(ctx context.Context, chatID, userID int64, projectKey, boardHint, sprintHint string) tgbotapi.MessageConfig {
	if boardHint == "" {
		return h.handleSprintProject(ctx, chatID, userID, projectKey)
	}

	lang := h.getLang(ctx, userID)

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected"))
	}

	boards, err := h.jiraAPI.GetBoards(ctx, user, projectKey)
	if err != nil {
		h.log.Error().Err(err).Str("project", projectKey).Msg("sprint: failed to get boards")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "sprint.boards_failed"))
	}

	if len(boards) == 0 {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "sprint.no_boards"))
	}

	boardID, found := matchBoard(boards, boardHint)
	if !found {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "sprint.board_not_found", boardHint))
	}

	// Board matched, but multiple candidates — show selection.
	if boardID == -1 {
		h.states.Set(userID, "sprint_board", map[string]string{"project": projectKey})

		matched := filterBoards(boards, boardHint)
		rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(matched))
		for _, b := range matched {
			data := fmt.Sprintf("sprint_board:%d", b.ID)
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(b.Name, data),
			))
		}
		msg := tgbotapi.NewMessage(chatID, locale.T(lang, "sprint.choose_board"))
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
		return msg
	}

	if sprintHint == "" {
		return h.handleSprintBoard(ctx, chatID, userID, boardID)
	}

	return h.handleSprintBoardWithHint(ctx, chatID, userID, boardID, sprintHint)
}

// matchBoard finds a board by ID or name. Returns (boardID, true) on unique match,
// (-1, true) if multiple partial matches, (0, false) if not found.
func matchBoard(boards []jira.Board, hint string) (int, bool) {
	if id, err := strconv.Atoi(hint); err == nil {
		for _, b := range boards {
			if b.ID == id {
				return b.ID, true
			}
		}
		return 0, false
	}

	hintLower := strings.ToLower(hint)
	var matched []jira.Board
	for _, b := range boards {
		if strings.EqualFold(b.Name, hintLower) {
			return b.ID, true
		}
		if strings.Contains(strings.ToLower(b.Name), hintLower) {
			matched = append(matched, b)
		}
	}

	if len(matched) == 1 {
		return matched[0].ID, true
	}
	if len(matched) > 1 {
		return -1, true
	}
	return 0, false
}

func filterBoards(boards []jira.Board, hint string) []jira.Board {
	hintLower := strings.ToLower(hint)
	var result []jira.Board
	for _, b := range boards {
		if strings.Contains(strings.ToLower(b.Name), hintLower) {
			result = append(result, b)
		}
	}
	return result
}

// handleSprintBoardWithHint fetches sprints and matches by hint (name or ID).
func (h *Handler) handleSprintBoardWithHint(ctx context.Context, chatID, userID int64, boardID int, sprintHint string) tgbotapi.MessageConfig {
	lang := h.getLang(ctx, userID)

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected"))
	}

	sprints, err := h.jiraAPI.GetSprints(ctx, user, boardID)
	if err != nil {
		h.log.Error().Err(err).Int("board_id", boardID).Msg("sprint: failed to get sprints")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "sprint.sprints_failed"))
	}

	if len(sprints) == 0 {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "sprint.no_sprints"))
	}

	// Try matching by sprint ID.
	if id, parseErr := strconv.Atoi(sprintHint); parseErr == nil {
		for _, s := range sprints {
			if s.ID == id {
				return h.handleSprintReport(ctx, chatID, userID, s.ID, boardID)
			}
		}
		return tgbotapi.NewMessage(chatID, locale.T(lang, "sprint.sprint_not_found", sprintHint))
	}

	// Match by name (case-insensitive substring).
	hintLower := strings.ToLower(sprintHint)
	var matched []jira.Sprint
	for _, s := range sprints {
		if strings.EqualFold(s.Name, hintLower) {
			return h.handleSprintReport(ctx, chatID, userID, s.ID, boardID)
		}
		if strings.Contains(strings.ToLower(s.Name), hintLower) {
			matched = append(matched, s)
		}
	}

	if len(matched) == 1 {
		return h.handleSprintReport(ctx, chatID, userID, matched[0].ID, boardID)
	}

	if len(matched) > 1 {
		h.states.Set(userID, "sprint_sprint", map[string]string{"board_id": strconv.Itoa(boardID)})

		if len(matched) > sprintListLimit {
			matched = matched[:sprintListLimit]
		}

		rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(matched))
		for _, s := range matched {
			label := s.Name
			if s.State == "active" {
				label += " 🟢"
			}
			data := fmt.Sprintf("sprint_report:%d:%d", s.ID, boardID)
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(label, data),
			))
		}
		msg := tgbotapi.NewMessage(chatID, locale.T(lang, "sprint.choose_sprint"))
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
		return msg
	}

	return tgbotapi.NewMessage(chatID, locale.T(lang, "sprint.sprint_not_found", sprintHint))
}

// handleSprintProject finds boards for a project and shows selection.
func (h *Handler) handleSprintProject(ctx context.Context, chatID, userID int64, projectKey string) tgbotapi.MessageConfig {
	lang := h.getLang(ctx, userID)

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected"))
	}

	boards, err := h.jiraAPI.GetBoards(ctx, user, projectKey)
	if err != nil {
		h.log.Error().Err(err).Str("project", projectKey).Msg("sprint: failed to get boards")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "sprint.boards_failed"))
	}

	if len(boards) == 0 {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "sprint.no_boards"))
	}

	// If single board, skip selection and show sprints directly.
	if len(boards) == 1 {
		return h.handleSprintBoard(ctx, chatID, userID, boards[0].ID)
	}

	h.states.Set(userID, "sprint_board", map[string]string{"project": projectKey})

	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(boards))
	for _, b := range boards {
		data := fmt.Sprintf("sprint_board:%d", b.ID)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.Name, data),
		))
	}

	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "sprint.choose_board"))
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	return msg
}

// handleSprintBoard fetches sprints for a board and shows selection.
func (h *Handler) handleSprintBoard(ctx context.Context, chatID, userID int64, boardID int) tgbotapi.MessageConfig {
	lang := h.getLang(ctx, userID)

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected"))
	}

	sprints, err := h.jiraAPI.GetSprints(ctx, user, boardID)
	if err != nil {
		h.log.Error().Err(err).Int("board_id", boardID).Msg("sprint: failed to get sprints")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "sprint.sprints_failed"))
	}

	if len(sprints) == 0 {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "sprint.no_sprints"))
	}

	h.states.Set(userID, "sprint_sprint", map[string]string{"board_id": strconv.Itoa(boardID)})

	if len(sprints) > sprintListLimit {
		sprints = sprints[:sprintListLimit]
	}

	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(sprints))
	for _, s := range sprints {
		label := s.Name
		if s.State == "active" {
			label += " 🟢"
		}
		data := fmt.Sprintf("sprint_report:%d:%d", s.ID, boardID)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, data),
		))
	}

	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "sprint.choose_sprint"))
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	return msg
}

const (
	velocitySprintCount = 3
)

// handleSprintReport generates the sprint report with metrics only.
func (h *Handler) handleSprintReport(ctx context.Context, chatID, userID int64, sprintID, boardID int) tgbotapi.MessageConfig {
	lang := h.getLang(ctx, userID)

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected"))
	}

	var sprint *jira.Sprint
	if s, sprintErr := h.jiraAPI.GetSprint(ctx, user, sprintID); sprintErr == nil {
		sprint = s
	}

	sprintName := ""
	sprintGoal := ""
	var sprintStart, sprintEnd time.Time
	isActiveSprint := false
	if sprint != nil {
		sprintName = sprint.Name
		sprintGoal = sprint.Goal
		isActiveSprint = sprint.State == "active"
		if sprint.StartDate != "" {
			sprintStart, _ = parseJiraTime(sprint.StartDate)
		}
		if sprint.EndDate != "" {
			sprintEnd, _ = parseJiraTime(sprint.EndDate)
		}
	}

	jql := fmt.Sprintf("sprint = %d ORDER BY status ASC", sprintID)
	result, err := h.jiraAPI.SearchIssuesForSprintReport(ctx, user, jql, sprintIssueMax, user.AssigneeFieldID)
	if err != nil {
		h.log.Error().Err(err).Int("sprint_id", sprintID).Msg("sprint: failed to search issues")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "sprint.report_failed"))
	}

	if len(result.Issues) == 0 {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "sprint.no_issues"))
	}

	typeNames := make(map[string]int)
	for i := range result.Issues {
		name := "Other"
		if result.Issues[i].Fields.IssueType != nil {
			name = result.Issues[i].Fields.IssueType.Name
		}
		typeNames[name]++
	}
	h.log.Debug().
		Strs("sprint_issue_types", user.SprintIssueTypes).
		Int("issue_count", len(result.Issues)).
		Interface("types_in_sprint", typeNames).
		Msg("sprint report: generating with filter")

	// Compute changelog-based metrics.
	filterSet := make(map[string]bool, len(user.SprintIssueTypes))
	for _, t := range user.SprintIssueTypes {
		filterSet[t] = true
	}
	var clMetrics *changelogMetrics
	if !sprintStart.IsZero() && sprintName != "" {
		clMetrics = computeChangelogMetrics(result.Issues, sprintName, sprintStart, filterSet)
	}

	// Compute velocity (requires boardID).
	var vel *velocityData
	if boardID > 0 {
		vel = h.computeVelocity(ctx, user, boardID, sprintID, result.Issues, filterSet)
	}

	var fc *forecastData
	if isActiveSprint && !sprintStart.IsZero() && !sprintEnd.IsZero() {
		fc = &forecastData{start: sprintStart, end: sprintEnd}
	}

	useCustomAssignee := user.AssigneeFieldID != ""
	text := formatSprintReport(lang, sprintName, sprintGoal, result.Issues, user.SprintIssueTypes, useCustomAssignee, clMetrics, vel, fc)

	// Split long messages for Telegram's 4096-char limit.
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

type issueStats struct {
	total      int
	done       int
	inProgress int
	hold       int
	todo       int
	sp         float64
	spDone     float64
}

type forecastData struct {
	start time.Time
	end   time.Time
}

func formatSprintReport(lang locale.Lang, sprintName, sprintGoal string, issues []jira.Issue, issueTypeFilter []string, useCustomAssignee bool, clm *changelogMetrics, vel *velocityData, fc *forecastData) string {
	var sb strings.Builder

	filterSet := make(map[string]bool, len(issueTypeFilter))
	for _, t := range issueTypeFilter {
		filterSet[t] = true
	}
	isFiltered := len(filterSet) > 0

	var overall issueStats
	byType := make(map[string]*issueStats)
	byAssignee := make(map[string]*issueStats)
	byPriority := make(map[string]*issueStats)
	bugTotal := 0
	var unestimated []string
	var overdue []string
	today := time.Now().Truncate(24 * time.Hour)

	for i := range issues {
		typeName := "Other"
		if issues[i].Fields.IssueType != nil {
			typeName = issues[i].Fields.IssueType.Name
		}

		cat := statusCategory(&issues[i])
		sp := float64(0)
		if issues[i].Fields.StoryPoints != nil {
			sp = *issues[i].Fields.StoryPoints
		}

		// Always track by-type stats for all issues.
		ts := byType[typeName]
		if ts == nil {
			ts = &issueStats{}
			byType[typeName] = ts
		}
		ts.total++
		ts.sp += sp
		if cat == "done" {
			ts.done++
			ts.spDone += sp
		}

		// Overall and by-assignee stats only for filtered types (or all if no filter).
		if isFiltered && !filterSet[typeName] {
			continue
		}

		overall.total++
		overall.sp += sp
		switch cat {
		case "done":
			overall.done++
			overall.spDone += sp
		case "indeterminate":
			overall.inProgress++
		case "hold":
			overall.hold++
		default:
			overall.todo++
		}

		// Bug ratio.
		if strings.EqualFold(typeName, "Bug") {
			bugTotal++
		}

		// Priority distribution.
		prioName := "None"
		if issues[i].Fields.Priority != nil { //nolint:gosec // i is always a valid index
			prioName = issues[i].Fields.Priority.Name
		}
		ps := byPriority[prioName]
		if ps == nil {
			ps = &issueStats{}
			byPriority[prioName] = ps
		}
		ps.total++
		ps.sp += sp
		if cat == "done" {
			ps.done++
			ps.spDone += sp
		}

		// Unestimated issues.
		if issues[i].Fields.StoryPoints == nil { //nolint:gosec // i is always a valid index
			unestimated = append(unestimated, issues[i].Key) //nolint:gosec // i is always a valid index
		}

		// Overdue issues (not done, due date in the past).
		if cat != "done" && issues[i].Fields.DueDate != "" { //nolint:gosec // i is always a valid index
			if due, parseErr := time.Parse("2006-01-02", issues[i].Fields.DueDate); parseErr == nil {
				if due.Before(today) {
					overdue = append(overdue, issues[i].Key) //nolint:gosec // i is always a valid index
				}
			}
		}

		assignee := locale.T(lang, "sprint.unassigned")
		if useCustomAssignee && issues[i].Fields.CustomAssignee != nil { //nolint:gosec // i is always a valid index
			assignee = issues[i].Fields.CustomAssignee.DisplayName //nolint:gosec // i is always a valid index
		} else if !useCustomAssignee && issues[i].Fields.Assignee != nil { //nolint:gosec // i is always a valid index
			assignee = issues[i].Fields.Assignee.DisplayName //nolint:gosec // i is always a valid index
		}
		as := byAssignee[assignee]
		if as == nil {
			as = &issueStats{}
			byAssignee[assignee] = as
		}
		as.total++
		as.sp += sp
		switch cat {
		case "done":
			as.done++
			as.spDone += sp
		case "indeterminate":
			as.inProgress++
		case "hold":
			as.hold++
		default:
			as.todo++
		}
	}

	// Header.
	sb.WriteString("📊 *")
	sb.WriteString(locale.T(lang, "sprint.report_title"))
	sb.WriteString("*")
	if sprintName != "" {
		sb.WriteString(": ")
		sb.WriteString(format.EscapeMarkdown(sprintName))
	}
	sb.WriteString("\n")
	if isFiltered {
		sb.WriteString("🏷 _")
		sb.WriteString(locale.T(lang, "sprint.filtered", strings.Join(issueTypeFilter, ", ")))
		sb.WriteString("_\n")
	}

	// Sprint goal.
	if sprintGoal != "" {
		sb.WriteString("🎯 _")
		sb.WriteString(format.EscapeMarkdown(sprintGoal))
		sb.WriteString("_\n")
	}
	sb.WriteString("\n")

	// Overall.
	fmt.Fprintf(&sb, "%s: *%d*\n", locale.T(lang, "sprint.total"), overall.total)
	fmt.Fprintf(&sb, "✅ %s: *%d*", locale.T(lang, "sprint.done"), overall.done)
	if overall.total > 0 {
		fmt.Fprintf(&sb, " (%d%%)", overall.done*100/overall.total)
	}
	sb.WriteString("\n")
	fmt.Fprintf(&sb, "🔄 %s: *%d*\n", locale.T(lang, "sprint.in_progress"), overall.inProgress)
	if overall.hold > 0 {
		fmt.Fprintf(&sb, "⏸ %s: *%d*\n", locale.T(lang, "sprint.hold"), overall.hold)
	}
	fmt.Fprintf(&sb, "📋 %s: *%d*\n", locale.T(lang, "sprint.todo"), overall.todo)

	// Bug ratio.
	if bugTotal > 0 {
		fmt.Fprintf(&sb, "🐛 %s: *%d*/%d", locale.T(lang, "sprint.bug_ratio"), bugTotal, overall.total)
		if overall.total > 0 {
			fmt.Fprintf(&sb, " (%d%%)", bugTotal*100/overall.total)
		}
		sb.WriteString("\n")
	}

	if overall.sp > 0 {
		sb.WriteString("\n")
		fmt.Fprintf(&sb, "🎯 *Story Points:* %.0f / %.0f", overall.spDone, overall.sp)
		fmt.Fprintf(&sb, " (%d%%)\n", int(overall.spDone*100/overall.sp))
	}

	if overall.total > 0 {
		sb.WriteString("\n")
		writeProgressBar(&sb, overall.done, overall.total)
	}

	// Unestimated issues.
	if len(unestimated) > 0 {
		sb.WriteString("\n\n")
		fmt.Fprintf(&sb, "❓ *%s:* %d", locale.T(lang, "sprint.unestimated"), len(unestimated))
		if overall.total > 0 {
			fmt.Fprintf(&sb, " (%d%%)", len(unestimated)*100/overall.total)
		}
		sb.WriteString("\n")
		writeIssueKeyList(&sb, unestimated, lang)
	}

	// Overdue issues.
	if len(overdue) > 0 {
		sb.WriteString("\n")
		fmt.Fprintf(&sb, "⏰ *%s:* %d\n", locale.T(lang, "sprint.overdue"), len(overdue))
		writeIssueKeyList(&sb, overdue, lang)
	}

	// By type (sorted by total desc).
	sb.WriteString("\n\n")
	sb.WriteString("📝 *")
	sb.WriteString(locale.T(lang, "sprint.by_type"))
	sb.WriteString("*\n")
	typeKeys := sortedMapKeys(byType, func(a, b string) bool {
		return byType[a].total > byType[b].total
	})
	for _, typeName := range typeKeys {
		if isFiltered && !filterSet[typeName] {
			continue
		}
		ts := byType[typeName]
		fmt.Fprintf(&sb, "• %s: %d/%d ✅", typeName, ts.done, ts.total)
		if ts.sp > 0 {
			fmt.Fprintf(&sb, " (%.0f/%.0f SP)", ts.spDone, ts.sp)
		}
		sb.WriteString("\n")
	}

	// By priority (sorted by severity).
	if len(byPriority) > 0 {
		sb.WriteString("\n🔥 *")
		sb.WriteString(locale.T(lang, "sprint.by_priority"))
		sb.WriteString("*\n")
		prioKeys := sortedMapKeys(byPriority, func(a, b string) bool {
			return priorityOrder(a) < priorityOrder(b)
		})
		for _, prioName := range prioKeys {
			ps := byPriority[prioName]
			fmt.Fprintf(&sb, "• %s: %d/%d ✅", prioName, ps.done, ps.total)
			if ps.sp > 0 {
				fmt.Fprintf(&sb, " (%.0f/%.0f SP)", ps.spDone, ps.sp)
			}
			sb.WriteString("\n")
		}
	}

	// By assignee (sorted by total desc, then alphabetical).
	sb.WriteString("\n👥 *")
	sb.WriteString(locale.T(lang, "sprint.by_assignee"))
	sb.WriteString("*\n")
	assigneeKeys := sortedMapKeys(byAssignee, func(a, b string) bool {
		if byAssignee[a].total != byAssignee[b].total {
			return byAssignee[a].total > byAssignee[b].total
		}
		return a < b
	})
	for _, name := range assigneeKeys {
		as := byAssignee[name]
		fmt.Fprintf(&sb, "• %s: %d/%d ✅", name, as.done, as.total)
		if as.sp > 0 {
			fmt.Fprintf(&sb, " (%.0f/%.0f SP)", as.spDone, as.sp)
		}
		if as.inProgress > 0 {
			fmt.Fprintf(&sb, ", 🔄%d", as.inProgress)
		}
		if as.hold > 0 {
			fmt.Fprintf(&sb, ", ⏸%d", as.hold)
		}
		sb.WriteString("\n")
	}

	// Advanced metrics.
	if vel != nil && (vel.currentSP > 0 || len(vel.history) > 0) {
		sb.WriteString("\n📈 *")
		sb.WriteString(locale.T(lang, "sprint.velocity"))
		sb.WriteString(":* ")
		fmt.Fprintf(&sb, "%.0f SP", vel.currentSP)
		if len(vel.history) > 0 {
			fmt.Fprintf(&sb, " | %s: %.0f SP", locale.T(lang, "sprint.velocity_avg", len(vel.history)), vel.avgSP)
			if vel.trend >= 0 {
				fmt.Fprintf(&sb, " | +%d%%", vel.trend)
			} else {
				fmt.Fprintf(&sb, " | %d%%", vel.trend)
			}
		}
		sb.WriteString("\n")
	}

	if clm != nil {
		if len(clm.scopeCreepKeys) > 0 {
			sb.WriteString("\n🔀 *")
			sb.WriteString(locale.T(lang, "sprint.scope_creep"))
			sb.WriteString(":* ")
			fmt.Fprintf(&sb, "%d", len(clm.scopeCreepKeys))
			if clm.scopeCreepSP > 0 {
				fmt.Fprintf(&sb, " (%.0f SP)", clm.scopeCreepSP)
			}
			sb.WriteString("\n")
			writeIssueKeyList(&sb, clm.scopeCreepKeys, lang)
		}

		if len(clm.carryOverKeys) > 0 {
			sb.WriteString("\n♻️ *")
			sb.WriteString(locale.T(lang, "sprint.carry_over"))
			sb.WriteString(":* ")
			fmt.Fprintf(&sb, "%d", len(clm.carryOverKeys))
			if clm.carryOverSP > 0 {
				fmt.Fprintf(&sb, " (%.0f SP)", clm.carryOverSP)
			}
			sb.WriteString("\n")
			writeIssueKeyList(&sb, clm.carryOverKeys, lang)
		}

		if clm.committedSP > 0 {
			sb.WriteString("\n📊 *")
			sb.WriteString(locale.T(lang, "sprint.commitment"))
			sb.WriteString(":* ")
			fmt.Fprintf(&sb, "%.0f SP → %.0f SP", clm.committedSP, clm.completedSP)
			if clm.committedSP > 0 {
				fmt.Fprintf(&sb, " (%d%%)", int(clm.completedSP*100/clm.committedSP))
			}
			sb.WriteString("\n")
		}

		if clm.cycleCount > 0 {
			sb.WriteString("\n⏱ *")
			sb.WriteString(locale.T(lang, "sprint.cycle_time"))
			sb.WriteString(":* ")
			sb.WriteString(locale.T(lang, "sprint.cycle_time_avg", formatDuration(clm.avgCycleHours), clm.cycleCount))
			sb.WriteString("\n")
		}

		if clm.blockedCount > 0 {
			sb.WriteString("\n⏸ *")
			sb.WriteString(locale.T(lang, "sprint.blocked_time"))
			sb.WriteString(":* ")
			sb.WriteString(locale.T(lang, "sprint.blocked_detail", formatDuration(clm.totalBlockedH), formatDuration(clm.avgBlockedH), clm.blockedCount))
			sb.WriteString("\n")
		}
	}

	// Completion forecast (active sprints only).
	if fc != nil && overall.sp > 0 {
		now := time.Now()
		totalDays := fc.end.Sub(fc.start).Hours() / 24
		daysElapsed := now.Sub(fc.start).Hours() / 24
		daysLeft := int(math.Ceil(fc.end.Sub(now).Hours() / 24))

		if daysElapsed > 0 && totalDays > 0 && daysLeft > 0 {
			dailyVelocity := overall.spDone / daysElapsed
			projectedSP := dailyVelocity * totalDays
			remainingSP := overall.sp - overall.spDone

			sb.WriteString("\n📅 *")
			sb.WriteString(locale.T(lang, "sprint.forecast"))
			sb.WriteString(":* ")
			fmt.Fprintf(&sb, "%.0f SP → %.0f SP", remainingSP, projectedSP)
			sb.WriteString(" (")
			sb.WriteString(locale.T(lang, "sprint.days_left", daysLeft))
			sb.WriteString(") ")
			if projectedSP >= overall.sp {
				sb.WriteString("✅ _")
				sb.WriteString(locale.T(lang, "sprint.forecast_on_track"))
				sb.WriteString("_")
			} else {
				sb.WriteString("⚠️ _")
				sb.WriteString(locale.T(lang, "sprint.forecast_at_risk"))
				sb.WriteString("_")
			}
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

func sortedMapKeys(m map[string]*issueStats, less func(a, b string) bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return less(keys[i], keys[j])
	})
	return keys
}

func priorityOrder(name string) int {
	switch strings.ToLower(name) {
	case "critical", "blocker":
		return 0
	case "highest":
		return 1
	case "high":
		return 2
	case "medium":
		return 3
	case "low":
		return 4
	case "lowest":
		return 5
	case "none":
		return 6
	default:
		return 5
	}
}

func splitMessage(text string, limit int) []string {
	if len(text) <= limit {
		return []string{text}
	}

	var parts []string
	sections := strings.Split(text, "\n\n")
	var current strings.Builder

	for _, section := range sections {
		// If adding this section would exceed limit, flush current.
		if current.Len() > 0 && current.Len()+len(section)+2 > limit {
			parts = append(parts, current.String())
			current.Reset()
		}
		// If single section exceeds limit, split by newlines.
		if len(section) > limit {
			lines := strings.Split(section, "\n")
			for _, line := range lines {
				if current.Len() > 0 && current.Len()+len(line)+1 > limit {
					parts = append(parts, current.String())
					current.Reset()
				}
				if current.Len() > 0 {
					current.WriteString("\n")
				}
				current.WriteString(line)
			}
			continue
		}
		if current.Len() > 0 {
			current.WriteString("\n\n")
		}
		current.WriteString(section)
	}

	if current.Len() > 0 {
		parts = append(parts, current.String())
	}

	return parts
}

type changelogMetrics struct {
	scopeCreepKeys []string
	scopeCreepSP   float64
	carryOverKeys  []string
	carryOverSP    float64
	committedSP    float64
	completedSP    float64
	avgCycleHours  float64
	cycleCount     int
	totalBlockedH  float64
	avgBlockedH    float64
	blockedCount   int
}

type velocityData struct {
	currentSP float64
	history   []float64 // SP done per previous sprint
	avgSP     float64
	trend     int // percentage change vs avg
}

// computeChangelogMetrics extracts scope creep, carry-over, cycle time, and blocked time from issue changelogs.
func computeChangelogMetrics(issues []jira.Issue, sprintName string, sprintStart time.Time, filterSet map[string]bool) *changelogMetrics {
	m := &changelogMetrics{}
	isFiltered := len(filterSet) > 0
	var totalCycleH float64

	for i := range issues {
		typeName := "Other"
		if issues[i].Fields.IssueType != nil {
			typeName = issues[i].Fields.IssueType.Name
		}
		if isFiltered && !filterSet[typeName] {
			continue
		}

		sp := float64(0)
		if issues[i].Fields.StoryPoints != nil {
			sp = *issues[i].Fields.StoryPoints
		}

		cat := statusCategory(&issues[i])
		if cat == "done" {
			m.completedSP += sp
		}

		isScopeCreep := false
		isCarryOver := false

		if issues[i].Changelog != nil {
			// Scope creep & carry-over: look for Sprint field changes.
			for _, h := range issues[i].Changelog.Histories {
				for _, item := range h.Items {
					if !strings.EqualFold(item.Field, "Sprint") {
						continue
					}
					if !strings.Contains(item.ToString, sprintName) {
						continue
					}
					// Issue was added to this sprint.
					ts, parseErr := parseJiraTime(h.Created)
					if parseErr != nil {
						continue
					}
					if ts.After(sprintStart) {
						isScopeCreep = true
					}
					// If fromString mentions another sprint, this is a carry-over.
					if item.FromString != "" && !strings.Contains(item.FromString, sprintName) {
						isCarryOver = true
					}
				}
			}

			// Cycle time: first "In Progress" → "Done".
			if cat == "done" {
				var firstInProgress time.Time
				var doneTime time.Time
				for _, h := range issues[i].Changelog.Histories { //nolint:gosec // i is always a valid index
					for _, item := range h.Items {
						if !strings.EqualFold(item.Field, "status") {
							continue
						}
						ts, parseErr := parseJiraTime(h.Created)
						if parseErr != nil {
							continue
						}
						toLower := strings.ToLower(item.ToString)
						if isInProgressStatus(toLower) && firstInProgress.IsZero() {
							firstInProgress = ts
						}
						if isDoneStatus(toLower) {
							doneTime = ts
						}
					}
				}
				if !firstInProgress.IsZero() && !doneTime.IsZero() && doneTime.After(firstInProgress) {
					hours := doneTime.Sub(firstInProgress).Hours()
					totalCycleH += hours
					m.cycleCount++
				}
			}

			// Blocked time: sum time in hold statuses.
			var blockedStart time.Time
			var issueBlockedH float64
			for _, h := range issues[i].Changelog.Histories { //nolint:gosec // i is always a valid index
				for _, item := range h.Items {
					if !strings.EqualFold(item.Field, "status") {
						continue
					}
					ts, parseErr := parseJiraTime(h.Created)
					if parseErr != nil {
						continue
					}
					toLower := strings.ToLower(item.ToString)
					fromLower := strings.ToLower(item.FromString)
					if isHoldStatus(toLower) && blockedStart.IsZero() {
						blockedStart = ts
					}
					if isHoldStatus(fromLower) && !blockedStart.IsZero() {
						issueBlockedH += ts.Sub(blockedStart).Hours()
						blockedStart = time.Time{}
					}
				}
			}
			if !blockedStart.IsZero() {
				issueBlockedH += time.Since(blockedStart).Hours()
			}
			if issueBlockedH > 0 {
				m.totalBlockedH += issueBlockedH
				m.blockedCount++
			}
		}

		if isScopeCreep {
			m.scopeCreepKeys = append(m.scopeCreepKeys, issues[i].Key)
			m.scopeCreepSP += sp
		}
		if isCarryOver {
			m.carryOverKeys = append(m.carryOverKeys, issues[i].Key)
			m.carryOverSP += sp
		}
	}

	// Finalize averages.
	if m.cycleCount > 0 {
		m.avgCycleHours = totalCycleH / float64(m.cycleCount)
	}
	if m.blockedCount > 0 {
		m.avgBlockedH = m.totalBlockedH / float64(m.blockedCount)
	}

	// Committed = total filtered SP - scope creep SP.
	for i := range issues {
		typeName := "Other"
		if issues[i].Fields.IssueType != nil {
			typeName = issues[i].Fields.IssueType.Name
		}
		if isFiltered && !filterSet[typeName] {
			continue
		}
		if issues[i].Fields.StoryPoints != nil {
			m.committedSP += *issues[i].Fields.StoryPoints
		}
	}
	m.committedSP -= m.scopeCreepSP

	return m
}

// computeVelocity fetches SP for previous sprints to calculate velocity trend.
func (h *Handler) computeVelocity(ctx context.Context, user *storage.User, boardID, currentSprintID int, currentIssues []jira.Issue, filterSet map[string]bool) *velocityData {
	sprints, err := h.jiraAPI.GetSprints(ctx, user, boardID)
	if err != nil {
		return nil
	}

	// Calculate current sprint done SP.
	isFiltered := len(filterSet) > 0
	currentDoneSP := float64(0)
	for i := range currentIssues {
		typeName := "Other"
		if currentIssues[i].Fields.IssueType != nil {
			typeName = currentIssues[i].Fields.IssueType.Name
		}
		if isFiltered && !filterSet[typeName] {
			continue
		}
		if statusCategory(&currentIssues[i]) == "done" && currentIssues[i].Fields.StoryPoints != nil {
			currentDoneSP += *currentIssues[i].Fields.StoryPoints
		}
	}

	// Find previous closed sprints.
	var prevSprints []jira.Sprint
	found := false
	for _, s := range sprints {
		if s.ID == currentSprintID {
			found = true
			continue
		}
		if found && s.State == "closed" {
			prevSprints = append(prevSprints, s)
			if len(prevSprints) >= velocitySprintCount {
				break
			}
		}
	}

	if len(prevSprints) == 0 {
		return &velocityData{currentSP: currentDoneSP}
	}

	// Fetch SP for previous sprints in parallel.
	type sprintSP struct {
		sp float64
	}
	results := make([]sprintSP, len(prevSprints))
	g, gctx := errgroup.WithContext(ctx)

	for idx, ps := range prevSprints {
		g.Go(func() error {
			jql := fmt.Sprintf("sprint = %d ORDER BY status ASC", ps.ID)
			res, searchErr := h.jiraAPI.SearchIssuesWithStoryPoints(gctx, user, jql, sprintIssueMax, "")
			if searchErr != nil {
				return nil //nolint:nilerr // non-fatal, skip this sprint
			}
			doneSP := float64(0)
			for j := range res.Issues {
				tn := "Other"
				if res.Issues[j].Fields.IssueType != nil {
					tn = res.Issues[j].Fields.IssueType.Name
				}
				if isFiltered && !filterSet[tn] {
					continue
				}
				if statusCategory(&res.Issues[j]) == "done" && res.Issues[j].Fields.StoryPoints != nil {
					doneSP += *res.Issues[j].Fields.StoryPoints
				}
			}
			results[idx].sp = doneSP
			return nil
		})
	}
	_ = g.Wait()

	vel := &velocityData{currentSP: currentDoneSP}
	totalPrev := float64(0)
	count := 0
	for _, r := range results {
		vel.history = append(vel.history, r.sp)
		totalPrev += r.sp
		count++
	}

	if count > 0 {
		vel.avgSP = totalPrev / float64(count)
		if vel.avgSP > 0 {
			vel.trend = int(math.Round((currentDoneSP - vel.avgSP) / vel.avgSP * 100))
		}
	}

	return vel
}

func isInProgressStatus(name string) bool {
	switch name {
	case "in progress", "in review", "in development", "review", "in testing":
		return true
	}
	return false
}

func isDoneStatus(name string) bool {
	switch name {
	case "done", "closed", "resolved", "completed":
		return true
	}
	return false
}

func isHoldStatus(name string) bool {
	switch name {
	case "hold", "on hold", "blocked", "suspended":
		return true
	}
	return false
}

func parseJiraTime(s string) (time.Time, error) {
	// Jira uses ISO 8601 format.
	for _, layout := range []string{
		"2006-01-02T15:04:05.000-0700",
		"2006-01-02T15:04:05.000Z",
		"2006-01-02T15:04:05Z",
		time.RFC3339,
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse jira time: %s", s)
}

func formatDuration(hours float64) string {
	if hours < 1 {
		return fmt.Sprintf("%dm", int(hours*60))
	}
	if hours < 24 {
		return fmt.Sprintf("%.1fh", hours)
	}
	return fmt.Sprintf("%.1fd", hours/24)
}

func statusCategory(issue *jira.Issue) string {
	if issue.Fields.Status == nil {
		return "new"
	}
	nameLower := strings.ToLower(issue.Fields.Status.Name)

	// Check hold by status name first — Jira has no built-in "hold" category,
	// so these statuses typically fall under "indeterminate".
	switch nameLower {
	case "hold", "on hold", "blocked", "suspended":
		return "hold"
	}

	if issue.Fields.Status.StatusCategory != nil {
		return issue.Fields.Status.StatusCategory.Key
	}
	switch nameLower {
	case "done", "closed", "resolved", "completed":
		return "done"
	case "in progress", "in review", "in development", "review":
		return "indeterminate"
	default:
		return "new"
	}
}

const issueKeyListMax = 5

func writeIssueKeyList(sb *strings.Builder, keys []string, lang locale.Lang) {
	show := keys
	if len(show) > issueKeyListMax {
		show = show[:issueKeyListMax]
	}
	sb.WriteString("  `")
	sb.WriteString(strings.Join(show, "`, `"))
	sb.WriteString("`")
	if len(keys) > issueKeyListMax {
		fmt.Fprintf(sb, " _%s_", locale.T(lang, "sprint.and_more", len(keys)-issueKeyListMax))
	}
	sb.WriteString("\n")
}

func writeProgressBar(sb *strings.Builder, done, total int) {
	const barLen = 20
	filled := done * barLen / total
	sb.WriteString("`[")
	for i := 0; i < barLen; i++ {
		if i < filled {
			sb.WriteString("█")
		} else {
			sb.WriteString("░")
		}
	}
	fmt.Fprintf(sb, "]` %d%%", done*100/total)
}

// handleSprintCallback routes sprint_board and sprint_report callbacks.
// Callback data format: sprint_report:sprintID:boardID or sprint_board:boardID.
func (h *Handler) handleSprintCallback(ctx context.Context, cq *tgbotapi.CallbackQuery, parts []string) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	chatID := cq.Message.Chat.ID
	userID := cq.From.ID

	if len(parts) < 2 {
		return
	}

	id, err := strconv.Atoi(parts[1])
	if err != nil {
		return
	}

	h.states.Clear(userID)

	var msg tgbotapi.MessageConfig
	switch parts[0] {
	case "sprint_board":
		msg = h.handleSprintBoard(ctx, chatID, userID, id)
	case "sprint_report":
		boardID := 0
		if len(parts) >= 3 {
			boardID, _ = strconv.Atoi(parts[2])
		}
		msg = h.handleSprintReport(ctx, chatID, userID, id, boardID)
	default:
		return
	}

	h.sendMessage(msg)
}
