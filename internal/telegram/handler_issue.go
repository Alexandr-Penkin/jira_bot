package telegram

import (
	"context"
	"fmt"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"SleepJiraBot/internal/format"
	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/locale"
)

func (h *Handler) handleIssue(ctx context.Context, chatID, userID int64, issueKey string) tgbotapi.MessageConfig {
	lang := h.getLang(ctx, userID)

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected"))
	}

	issue, err := h.jiraAPI.GetIssue(ctx, user, issueKey)
	if err != nil {
		h.log.Error().Err(err).Str("issue", issueKey).Msg("failed to get issue")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "issue.failed", issueKey))
	}

	msg := tgbotapi.NewMessage(chatID, formatIssue(lang, issue, user.JiraSiteURL))
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.DisableWebPagePreview = true
	return msg
}

func (h *Handler) handleList(ctx context.Context, chatID, userID int64, jql string) tgbotapi.MessageConfig {
	lang := h.getLang(ctx, userID)

	if jql == "" {
		jql = "assignee=currentUser() AND resolution=Unresolved ORDER BY updated DESC"
	}
	if len(jql) > maxJQLLen {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "list.jql_too_long", maxJQLLen))
	}

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected"))
	}

	result, err := h.jiraAPI.SearchIssues(ctx, user, jql, listMaxResults)
	if err != nil {
		h.log.Error().Err(err).Str("jql", jql).Msg("failed to search issues")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "list.failed"))
	}

	if len(result.Issues) == 0 {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "list.no_issues"))
	}

	count := len(result.Issues)
	text := locale.T(lang, "list.found", count)
	for i := range result.Issues {
		issue := &result.Issues[i]
		status := "?"
		if issue.Fields.Status != nil {
			status = issue.Fields.Status.Name
		}
		issueURL := fmt.Sprintf("%s/browse/%s", user.JiraSiteURL, issue.Key)
		text += fmt.Sprintf("[%s](%s) \\[%s] %s\n", issue.Key, issueURL, format.EscapeMarkdown(status), format.EscapeMarkdown(issue.Fields.Summary))
	}

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.DisableWebPagePreview = true
	return msg
}

func (h *Handler) handleComment(ctx context.Context, chatID, userID int64, issueKey, commentText string) tgbotapi.MessageConfig {
	lang := h.getLang(ctx, userID)

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected"))
	}

	if err = h.jiraAPI.AddComment(ctx, user, issueKey, commentText); err != nil {
		h.log.Error().Err(err).Str("issue", issueKey).Msg("failed to add comment")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "comment.failed", issueKey))
	}

	return tgbotapi.NewMessage(chatID, locale.T(lang, "comment.added", issueKey))
}

func (h *Handler) handleTransition(ctx context.Context, chatID, userID int64, issueKey string) tgbotapi.MessageConfig {
	lang := h.getLang(ctx, userID)

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected"))
	}

	transitions, err := h.jiraAPI.GetTransitions(ctx, user, issueKey)
	if err != nil {
		h.log.Error().Err(err).Str("issue", issueKey).Msg("failed to get transitions")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "transition.failed", issueKey))
	}

	if len(transitions) == 0 {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "transition.none", issueKey))
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, t := range transitions {
		callbackData := fmt.Sprintf("transition:%s:%s", issueKey, t.ID)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("%s → %s", t.Name, t.To.Name),
				callbackData,
			),
		))
	}

	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "transition.choose", issueKey))
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	return msg
}

func (h *Handler) handleAssign(ctx context.Context, chatID, userID int64, issueKey string) tgbotapi.MessageConfig {
	lang := h.getLang(ctx, userID)

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected"))
	}

	myself, err := h.jiraAPI.GetMyself(ctx, user)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to get jira user for assign")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "assign.me_failed"))
	}

	if err = h.jiraAPI.AssignIssue(ctx, user, issueKey, myself.AccountID); err != nil {
		h.log.Error().Err(err).Str("issue", issueKey).Msg("failed to assign issue")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "assign.failed", issueKey))
	}

	return tgbotapi.NewMessage(chatID, locale.T(lang, "assign.success", issueKey))
}

func (h *Handler) handleJiraLink(ctx context.Context, chatID, userID int64, issueKey string) {
	lang := h.getLang(ctx, userID)

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected")))
		return
	}

	issue, err := h.jiraAPI.GetIssue(ctx, user, issueKey)
	if err != nil {
		h.log.Error().Err(err).Str("issue", issueKey).Msg("failed to get issue from link")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "issue.failed", issueKey)))
		return
	}

	msg := tgbotapi.NewMessage(chatID, formatIssue(lang, issue, user.JiraSiteURL))
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.DisableWebPagePreview = true
	msg.ReplyMarkup = issueActionsKeyboard(lang, issueKey)
	h.sendMessage(msg)
}

func (h *Handler) handleIssueActionCallback(ctx context.Context, cq *tgbotapi.CallbackQuery, parts []string) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	if len(parts) < 3 {
		return
	}

	action := parts[1]
	issueKey := parts[2]
	chatID := cq.Message.Chat.ID
	userID := cq.From.ID
	lang := h.getLang(ctx, userID)

	switch action {
	case "comment":
		h.states.Set(userID, "comment_text", map[string]string{"issue_key": issueKey})
		h.sendPrompt(chatID, locale.T(lang, "comment.enter_text", issueKey), lang)
	case "transition":
		h.sendMessage(h.handleTransition(ctx, chatID, userID, issueKey))
	case "assign":
		h.sendMessage(withMenuButton(h.handleAssign(ctx, chatID, userID, issueKey), lang))
	case "watch":
		h.handleSubIssueInput(ctx, chatID, userID, issueKey, lang)
	}
}

func formatIssue(lang locale.Lang, issue *jira.Issue, siteURL string) string {
	f := issue.Fields

	status := "—"
	if f.Status != nil {
		status = f.Status.Name
	}
	priority := "—"
	if f.Priority != nil {
		priority = f.Priority.Name
	}
	assignee := locale.T(lang, "issue.unassigned")
	if f.Assignee != nil {
		assignee = f.Assignee.DisplayName
	}
	reporter := "—"
	if f.Reporter != nil {
		reporter = f.Reporter.DisplayName
	}
	issueType := "—"
	if f.IssueType != nil {
		issueType = f.IssueType.Name
	}

	issueURL := fmt.Sprintf("%s/browse/%s", siteURL, issue.Key)

	text := fmt.Sprintf(
		"*[%s](%s) %s*\n\n"+
			"%s: %s\n"+
			"%s: %s\n"+
			"%s: %s\n"+
			"%s: %s\n"+
			"%s: %s",
		issue.Key,
		issueURL,
		format.EscapeMarkdown(f.Summary),
		locale.T(lang, "issue.type"), format.EscapeMarkdown(issueType),
		locale.T(lang, "issue.status"), format.EscapeMarkdown(status),
		locale.T(lang, "issue.priority"), format.EscapeMarkdown(priority),
		locale.T(lang, "issue.assignee"), format.EscapeMarkdown(assignee),
		locale.T(lang, "issue.reporter"), format.EscapeMarkdown(reporter),
	)

	if f.DueDate != "" {
		text += fmt.Sprintf("\n%s: %s", locale.T(lang, "issue.due"), format.EscapeMarkdown(f.DueDate))
	}

	if len(f.Labels) > 0 {
		text += fmt.Sprintf("\n%s: %s", locale.T(lang, "issue.labels"), format.EscapeMarkdown(strings.Join(f.Labels, ", ")))
	}

	desc := f.Description.ExtractText()
	if desc != "" {
		desc = format.TruncateRunes(desc, descMaxLen)
		text += fmt.Sprintf("\n\n_%s_", format.EscapeMarkdown(desc))
	}

	return text
}
