package telegram

import (
	"context"
	"fmt"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"SleepJiraBot/internal/format"
	"SleepJiraBot/internal/locale"
	"SleepJiraBot/internal/storage"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// handleSubscribeMenu shows the subscription type chooser.
func (h *Handler) handleSubscribeMenu(chatID int64, lang locale.Lang) {
	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "sub.choose_type"))
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = subscribeTypeKeyboard(lang)
	h.sendMessage(msg)
}

func subscribeTypeKeyboard(lang locale.Lang) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.sub_my_new"), "sub:my_new_issues"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.sub_my_mentions"), "sub:my_mentions"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.sub_my_watched"), "sub:my_watched"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.sub_project"), "sub:project_updates"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.sub_issue"), "sub:issue_updates"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.sub_filter"), "sub:filter_updates"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.back"), "m:notif"),
		),
	)
}

// handleSubCallback routes sub:<type> callbacks.
func (h *Handler) handleSubCallback(ctx context.Context, cq *tgbotapi.CallbackQuery, parts []string) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	chatID := cq.Message.Chat.ID
	userID := cq.From.ID
	lang := h.getLang(ctx, userID)

	if len(parts) < 2 {
		return
	}

	subType := parts[1]

	switch subType {
	case storage.SubTypeMyNewIssues, storage.SubTypeMyMentions, storage.SubTypeMyWatched:
		// One-click subscriptions — no extra input needed.
		h.createSimpleSubscription(ctx, chatID, userID, subType, lang)

	case storage.SubTypeProjectUpdates:
		h.states.Set(userID, "sub_project", nil)
		h.sendPrompt(chatID, locale.T(lang, "sub.enter_project"), lang)

	case storage.SubTypeIssueUpdates:
		h.states.Set(userID, "sub_issue", nil)
		h.sendPrompt(chatID, locale.T(lang, "sub.enter_issue"), lang)

	case storage.SubTypeFilterUpdates:
		h.handleFilterSubscription(ctx, chatID, userID, lang)
	}
}

// handleFilterCallback routes sub_filter:<filterID> callbacks.
func (h *Handler) handleFilterCallback(ctx context.Context, cq *tgbotapi.CallbackQuery, parts []string) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	chatID := cq.Message.Chat.ID
	userID := cq.From.ID
	lang := h.getLang(ctx, userID)

	if len(parts) < 2 {
		return
	}

	filterID := parts[1]

	exists, err := h.subRepo.Exists(ctx, chatID, storage.SubTypeFilterUpdates, bson.M{"jira_filter_id": filterID})
	if err != nil {
		h.log.Error().Err(err).Msg("failed to check filter subscription existence")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "sub.failed")))
		return
	}
	if exists {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "sub.already_exists")))
		return
	}

	// Look up full filter details from Jira API to get name and JQL.
	user, authErr := h.requireAuth(ctx, userID)
	if authErr != nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected")))
		return
	}

	filters, fetchErr := h.jiraAPI.GetMyFilters(ctx, user)
	if fetchErr != nil {
		h.log.Error().Err(fetchErr).Msg("failed to fetch filters for subscription")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "sub.failed")))
		return
	}

	var filterName, filterJQL string
	for _, f := range filters {
		if f.ID == filterID {
			filterName = f.Name
			filterJQL = f.JQL
			break
		}
	}

	if filterJQL == "" {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "sub.filters_failed")))
		return
	}

	sub := &storage.Subscription{
		TelegramChatID:   chatID,
		TelegramUserID:   userID,
		SubscriptionType: storage.SubTypeFilterUpdates,
		JiraFilterID:     filterID,
		JiraFilterName:   filterName,
		JiraFilterJQL:    filterJQL,
	}

	if err := h.subRepo.Create(ctx, sub); err != nil {
		h.log.Error().Err(err).Msg("failed to create filter subscription")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "sub.failed")))
		return
	}
	if h.webhookMgr != nil {
		h.webhookMgr.RegisterForSubscription(ctx, sub)
	}

	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "sub.created_filter", filterName))
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = menuButtonKeyboard(lang)
	h.sendMessage(msg)
}

func (h *Handler) createSimpleSubscription(ctx context.Context, chatID, userID int64, subType string, lang locale.Lang) {
	exists, err := h.subRepo.Exists(ctx, chatID, subType, nil)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to check subscription existence")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "sub.failed")))
		return
	}
	if exists {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "sub.already_exists")))
		return
	}

	sub := &storage.Subscription{
		TelegramChatID:   chatID,
		TelegramUserID:   userID,
		SubscriptionType: subType,
	}

	if err := h.subRepo.Create(ctx, sub); err != nil {
		h.log.Error().Err(err).Msg("failed to create subscription")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "sub.failed")))
		return
	}
	if h.webhookMgr != nil {
		h.webhookMgr.RegisterForSubscription(ctx, sub)
	}

	label := subTypeLabel(lang, subType)
	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "sub.created", label))
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = menuButtonKeyboard(lang)
	h.sendMessage(msg)
}

func (h *Handler) handleSubProjectInput(ctx context.Context, chatID, userID int64, text string, lang locale.Lang) {
	projectKey := strings.ToUpper(strings.TrimSpace(text))
	if !validateProjectKey(projectKey) {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "watch.invalid_project_short")))
		return
	}

	h.createKeyedSubscription(ctx, chatID, userID, lang, &storage.Subscription{
		SubscriptionType: storage.SubTypeProjectUpdates,
		JiraProjectKey:   projectKey,
	}, bson.M{"jira_project_key": projectKey}, "sub.created_project", projectKey)
}

func (h *Handler) handleSubIssueInput(ctx context.Context, chatID, userID int64, text string, lang locale.Lang) {
	issueKey := strings.ToUpper(strings.TrimSpace(text))
	if !validateIssueKey(issueKey) {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "issue.invalid_key_short")))
		return
	}

	h.createKeyedSubscription(ctx, chatID, userID, lang, &storage.Subscription{
		SubscriptionType: storage.SubTypeIssueUpdates,
		JiraIssueKey:     issueKey,
	}, bson.M{"jira_issue_key": issueKey}, "sub.created_issue", issueKey)
}

func (h *Handler) createKeyedSubscription(ctx context.Context, chatID, userID int64, lang locale.Lang, sub *storage.Subscription, extra bson.M, successKey, successArg string) {
	exists, err := h.subRepo.Exists(ctx, chatID, sub.SubscriptionType, extra)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to check subscription existence")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "sub.failed")))
		return
	}
	if exists {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "sub.already_exists")))
		return
	}

	sub.TelegramChatID = chatID
	sub.TelegramUserID = userID

	if err := h.subRepo.Create(ctx, sub); err != nil {
		h.log.Error().Err(err).Msg("failed to create subscription")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "sub.failed")))
		return
	}
	if h.webhookMgr != nil {
		h.webhookMgr.RegisterForSubscription(ctx, sub)
	}

	msg := tgbotapi.NewMessage(chatID, locale.T(lang, successKey, successArg))
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = menuButtonKeyboard(lang)
	h.sendMessage(msg)
}

func (h *Handler) handleFilterSubscription(ctx context.Context, chatID, userID int64, lang locale.Lang) {
	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected")))
		return
	}

	filters, err := h.jiraAPI.GetMyFilters(ctx, user)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to get filters")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "sub.filters_failed")))
		return
	}

	if len(filters) == 0 {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "sub.no_filters")))
		return
	}

	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(filters)+1)
	for _, f := range filters {
		data := fmt.Sprintf("sub_filter:%s", f.ID)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(f.Name, data),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.cancel"), "a:cancel"),
	))

	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "sub.choose_filter"))
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	h.sendMessage(msg)
}

func (h *Handler) handleUnwatch(ctx context.Context, chatID int64, lang locale.Lang) tgbotapi.MessageConfig {
	// Fetch subs first so we can drop the corresponding Jira webhooks
	// before removing the local rows. Failing to fetch is non-fatal —
	// we still attempt the delete.
	if h.webhookMgr != nil {
		if subs, err := h.subRepo.GetByChat(ctx, chatID); err == nil {
			for i := range subs {
				h.webhookMgr.DeleteForSubscription(ctx, subs[i].TelegramUserID, subs[i].ID)
			}
		}
	}

	if err := h.subRepo.DeleteByChat(ctx, chatID); err != nil {
		h.log.Error().Err(err).Msg("failed to delete subscriptions")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "unwatch.failed"))
	}

	return tgbotapi.NewMessage(chatID, locale.T(lang, "unwatch.success"))
}

func (h *Handler) handleSubscriptions(ctx context.Context, chatID int64, lang locale.Lang) tgbotapi.MessageConfig {
	subs, err := h.subRepo.GetByChat(ctx, chatID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to get subscriptions")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "subs.failed"))
	}

	if len(subs) == 0 {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "subs.none"))
	}

	text := locale.T(lang, "subs.title")
	for i := range subs {
		label := subTypeLabel(lang, subs[i].SubscriptionType)
		detail := subDetail(&subs[i])
		if detail != "" {
			text += fmt.Sprintf("%d. %s — %s\n", i+1, format.EscapeMarkdown(label), format.EscapeMarkdown(detail))
		} else {
			text += fmt.Sprintf("%d. %s\n", i+1, format.EscapeMarkdown(label))
		}
	}

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	return msg
}

func subTypeLabel(lang locale.Lang, subType string) string {
	key := "sub.type_" + subType
	label := locale.T(lang, key)
	if label == key {
		return subType
	}
	return label
}

func subDetail(sub *storage.Subscription) string {
	switch sub.SubscriptionType {
	case storage.SubTypeProjectUpdates:
		return sub.JiraProjectKey
	case storage.SubTypeIssueUpdates:
		return sub.JiraIssueKey
	case storage.SubTypeFilterUpdates:
		return sub.JiraFilterName
	default:
		return ""
	}
}
