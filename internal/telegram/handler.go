package telegram

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog"

	"SleepJiraBot/internal/format"
	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/locale"
	"SleepJiraBot/internal/poller"
	"SleepJiraBot/internal/storage"
)

var issueKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9]+-\d+$`)
var jiraURLRe = regexp.MustCompile(`https?://[a-zA-Z0-9._-]+\.atlassian\.net/browse/([A-Z][A-Z0-9]+-\d+)`)

const (
	listMaxResults     = 15
	descMaxLen         = 300
	maxIssueKeyLen     = 32
	maxProjectKeyLen   = 20
	maxCommentLen      = 5000
	maxJQLLen          = 2000
	maxReportNameLen   = 100
	minCronIntervalMin = 5
)

type Handler struct {
	api              *tgbotapi.BotAPI
	oauth            *jira.OAuthClient
	jiraAPI          *jira.Client
	callbackServer   *jira.CallbackServer
	userRepo         *storage.UserRepo
	subRepo          *storage.SubscriptionRepo
	scheduleRepo     *storage.ScheduleRepo
	webhookMgr       *jira.WebhookManager
	onScheduleChange func()
	log              zerolog.Logger
	states           *stateManager
	adminID          int64
	pollerRef        *poller.Poller
}

func NewHandler(api *tgbotapi.BotAPI, oauth *jira.OAuthClient, jiraAPI *jira.Client, userRepo *storage.UserRepo, subRepo *storage.SubscriptionRepo, scheduleRepo *storage.ScheduleRepo, webhookMgr *jira.WebhookManager, log zerolog.Logger, adminID int64) *Handler {
	return &Handler{
		api:          api,
		oauth:        oauth,
		jiraAPI:      jiraAPI,
		userRepo:     userRepo,
		subRepo:      subRepo,
		scheduleRepo: scheduleRepo,
		webhookMgr:   webhookMgr,
		log:          log,
		states:       newStateManager(),
		adminID:      adminID,
	}
}

func (h *Handler) SetCallbackServer(cs *jira.CallbackServer) {
	h.callbackServer = cs
}

func (h *Handler) SetOnScheduleChange(fn func()) {
	h.onScheduleChange = fn
}

// --- Entry point ---

func (h *Handler) HandleUpdate(ctx context.Context, update tgbotapi.Update) {
	if update.CallbackQuery != nil {
		h.handleCallbackQuery(ctx, update.CallbackQuery)
		return
	}

	if update.Message == nil {
		return
	}

	if update.Message.IsCommand() {
		h.log.Debug().
			Str("command", update.Message.Command()).
			Str("from", update.Message.From.UserName).
			Msg("received command")
		msg := h.routeCommand(ctx, update.Message)
		h.sendMessage(msg)
		return
	}

	h.handleTextInput(ctx, update.Message)
}

// --- Command routing ---

func (h *Handler) routeCommand(ctx context.Context, message *tgbotapi.Message) tgbotapi.MessageConfig {
	chatID := message.Chat.ID
	userID := message.From.ID
	args := message.CommandArguments()

	switch message.Command() {
	case "start", "menu":
		return h.handleStart(chatID, userID, h.getLang(ctx, userID))
	case "help":
		return h.handleHelp(chatID, h.getLang(ctx, userID))
	case "lang":
		return h.handleLang(chatID, h.getLang(ctx, userID))
	case "connect":
		return h.handleConnect(ctx, chatID, userID)
	case "disconnect":
		return h.handleDisconnect(ctx, chatID, userID)
	case "me":
		return h.handleMe(ctx, chatID, userID)
	case "sprint":
		sprintArgs := strings.TrimSpace(args)
		if sprintArgs != "" {
			argParts := strings.SplitN(sprintArgs, " ", 3)
			projectKey := strings.ToUpper(argParts[0])
			boardHint := ""
			sprintHint := ""
			if len(argParts) > 1 {
				boardHint = strings.TrimSpace(argParts[1])
			}
			if len(argParts) > 2 {
				sprintHint = strings.TrimSpace(argParts[2])
			}
			return h.handleSprintFull(ctx, chatID, userID, projectKey, boardHint, sprintHint)
		}
		if user, _ := h.userRepo.GetByTelegramID(ctx, userID); user != nil && user.DefaultProject != "" {
			if user.DefaultBoardID != 0 {
				return h.handleSprintBoard(ctx, chatID, userID, user.DefaultBoardID)
			}
			return h.handleSprintProject(ctx, chatID, userID, user.DefaultProject)
		}
		h.states.Set(userID, "sprint_project", nil)
		h.handleSprintStart(chatID, h.getLang(ctx, userID))
		return tgbotapi.MessageConfig{}
	case "daily":
		if args := message.CommandArguments(); strings.TrimSpace(args) != "" {
			return h.handleDailySearch(ctx, chatID, userID, strings.TrimSpace(args))
		}
		return h.handleDaily(ctx, chatID, userID)
	case "issue":
		lang := h.getLang(ctx, userID)
		issueKey := strings.TrimSpace(strings.Split(args, " ")[0])
		if issueKey == "" {
			return tgbotapi.NewMessage(chatID, locale.T(lang, "issue.usage"))
		}
		if !validateIssueKey(issueKey) {
			return tgbotapi.NewMessage(chatID, locale.T(lang, "issue.invalid_key"))
		}
		return h.handleIssue(ctx, chatID, userID, issueKey)
	case "filters":
		return h.handleFilters(ctx, chatID, userID)
	case "list":
		return h.handleList(ctx, chatID, userID, args)
	case "comment":
		lang := h.getLang(ctx, userID)
		parts := strings.SplitN(args, " ", 2)
		if len(parts) < 2 {
			return tgbotapi.NewMessage(chatID, locale.T(lang, "comment.usage"))
		}
		if !validateIssueKey(parts[0]) {
			return tgbotapi.NewMessage(chatID, locale.T(lang, "issue.invalid_key"))
		}
		if len(parts[1]) > maxCommentLen {
			return tgbotapi.NewMessage(chatID, locale.T(lang, "comment.too_long", maxCommentLen))
		}
		return h.handleComment(ctx, chatID, userID, parts[0], parts[1])
	case "transition":
		lang := h.getLang(ctx, userID)
		issueKey := strings.TrimSpace(strings.Split(args, " ")[0])
		if issueKey == "" {
			return tgbotapi.NewMessage(chatID, locale.T(lang, "transition.usage"))
		}
		if !validateIssueKey(issueKey) {
			return tgbotapi.NewMessage(chatID, locale.T(lang, "issue.invalid_key"))
		}
		return h.handleTransition(ctx, chatID, userID, issueKey)
	case "assign":
		lang := h.getLang(ctx, userID)
		issueKey := strings.TrimSpace(strings.Split(args, " ")[0])
		if issueKey == "" {
			return tgbotapi.NewMessage(chatID, locale.T(lang, "assign.usage"))
		}
		if !validateIssueKey(issueKey) {
			return tgbotapi.NewMessage(chatID, locale.T(lang, "issue.invalid_key"))
		}
		return h.handleAssign(ctx, chatID, userID, issueKey)
	case "watch":
		lang := h.getLang(ctx, userID)
		h.handleSubscribeMenu(chatID, lang)
		return tgbotapi.MessageConfig{}
	case "unwatch":
		return h.handleUnwatch(ctx, chatID, h.getLang(ctx, userID))
	case "subscriptions":
		return h.handleSubscriptions(ctx, chatID, h.getLang(ctx, userID))
	case "schedule":
		lang := h.getLang(ctx, userID)
		if args == "" {
			msg := tgbotapi.NewMessage(chatID, locale.T(lang, "schedule.usage"))
			msg.ParseMode = tgbotapi.ModeMarkdown
			return msg
		}
		return h.handleSchedule(ctx, chatID, userID, args)
	case "unschedule":
		return h.handleUnschedule(ctx, chatID, userID)
	case "schedules":
		return h.handleSchedules(ctx, chatID, userID)
	case "admin":
		lang := h.getLang(ctx, userID)
		if !h.isAdmin(userID) {
			return tgbotapi.NewMessage(chatID, locale.T(lang, "admin.not_authorized"))
		}
		return h.handleAdminCommand(chatID, lang)
	default:
		lang := h.getLang(ctx, userID)
		return tgbotapi.NewMessage(chatID, locale.T(lang, "unknown_command"))
	}
}

// --- Messaging helpers ---

func (h *Handler) sendMessage(msg tgbotapi.MessageConfig) {
	if msg.Text == "" {
		return
	}
	if _, err := h.api.Send(msg); err != nil {
		if msg.ParseMode != "" {
			h.log.Warn().Err(err).Msg("markdown send failed, retrying without parse mode")
			msg.ParseMode = ""
			msg.Text = format.StripMarkdownEscapes(msg.Text)
			if _, retryErr := h.api.Send(msg); retryErr != nil {
				h.log.Error().Err(retryErr).Msg("failed to send message (retry)")
			}
			return
		}
		h.log.Error().Err(err).Msg("failed to send message")
	}
}

func (h *Handler) sendPrompt(chatID int64, text string, lang locale.Lang) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = cancelKeyboard(lang)
	h.sendMessage(msg)
}

func withMenuButton(msg tgbotapi.MessageConfig, lang locale.Lang) tgbotapi.MessageConfig {
	if msg.ReplyMarkup == nil {
		msg.ReplyMarkup = menuButtonKeyboard(lang)
	}
	return msg
}

// --- Callback routing ---

func (h *Handler) handleCallbackQuery(ctx context.Context, cq *tgbotapi.CallbackQuery) {
	parts := strings.SplitN(cq.Data, ":", 4)

	// Handle callbacks that have no colon-separated parameters.
	switch parts[0] {
	case "it_save":
		h.handleIssueTypeSave(ctx, cq)
		return
	case "it_clear":
		h.handleIssueTypeClear(ctx, cq)
		return
	}

	if len(parts) < 2 {
		cb := tgbotapi.NewCallback(cq.ID, "Unknown action")
		_, _ = h.api.Request(cb)
		return
	}

	switch parts[0] {
	case "m":
		h.handleMenuCallback(ctx, cq, parts[1])
	case "a":
		h.handleActionCallback(ctx, cq, parts[1])
	case "lang":
		h.handleLangCallback(ctx, cq, parts[1])
	case "transition":
		h.handleTransitionCallback(ctx, cq, parts)
	case "daily":
		h.handleDailyCallback(ctx, cq, parts)
	case "sub":
		h.handleSubCallback(ctx, cq, parts)
	case "sub_filter":
		h.handleFilterCallback(ctx, cq, parts)
	case "filters":
		h.handleFiltersCallback(ctx, cq, parts)
	case "defaults_board":
		h.handleDefaultsBoardCallback(ctx, cq, parts)
	case "sprint_board", "sprint_report":
		h.handleSprintCallback(ctx, cq, parts)
	case "it_toggle":
		if len(parts) >= 2 {
			h.handleIssueTypeToggle(ctx, cq, parts[1])
		}
	case "site_select":
		h.handleSiteSelectCallback(ctx, cq, parts)
	case "af_select":
		h.handleAssigneeFieldCallback(ctx, cq, parts)
	case "af_reset":
		h.handleAssigneeFieldReset(ctx, cq)
	case "sp_select":
		h.handleStoryPointsFieldCallback(ctx, cq, parts)
	case "sp_reset":
		h.handleStoryPointsFieldReset(ctx, cq)
	case "djql_done":
		h.handleDailyJQLEdit(ctx, cq, "daily_jql_done", "daily_jql.enter_done")
	case "djql_doing":
		h.handleDailyJQLEdit(ctx, cq, "daily_jql_doing", "daily_jql.enter_doing")
	case "djql_plan":
		h.handleDailyJQLEdit(ctx, cq, "daily_jql_plan", "daily_jql.enter_plan")
	case "djql_reset":
		h.handleDailyJQLReset(ctx, cq)
	case "issue_action":
		h.handleIssueActionCallback(ctx, cq, parts)
	case "adm":
		if h.isAdmin(cq.From.ID) {
			h.handleAdminCallback(ctx, cq, parts[1])
		}
	case "adm_user":
		if h.isAdmin(cq.From.ID) {
			h.handleAdminUserCallback(ctx, cq, parts)
		}
	case "adm_page":
		if h.isAdmin(cq.From.ID) {
			h.handleAdminPageCallback(ctx, cq, parts)
		}
	case "adm_disc":
		if h.isAdmin(cq.From.ID) {
			h.handleAdminDisconnect(ctx, cq, parts)
		}
	case "adm_del":
		if h.isAdmin(cq.From.ID) {
			h.handleAdminDelete(ctx, cq, parts)
		}
	default:
		cb := tgbotapi.NewCallback(cq.ID, "Unknown action")
		_, _ = h.api.Request(cb)
	}
}

func (h *Handler) handleMenuCallback(ctx context.Context, cq *tgbotapi.CallbackQuery, menu string) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	h.states.Clear(cq.From.ID)
	lang := h.getLang(ctx, cq.From.ID)

	var text string
	var keyboard tgbotapi.InlineKeyboardMarkup

	switch menu {
	case "main":
		text = locale.T(lang, "menu.main")
		if h.isAdmin(cq.From.ID) {
			keyboard = mainMenuKeyboardAdmin(lang)
		} else {
			keyboard = mainMenuKeyboard(lang)
		}
		edit := tgbotapi.NewEditMessageTextAndMarkup(
			cq.Message.Chat.ID, cq.Message.MessageID, text, keyboard)
		edit.ParseMode = tgbotapi.ModeMarkdown
		if _, err := h.api.Send(edit); err != nil {
			msg := tgbotapi.NewMessage(cq.Message.Chat.ID, text)
			msg.ParseMode = tgbotapi.ModeMarkdown
			msg.ReplyMarkup = keyboard
			h.sendMessage(msg)
		}
		return
	case "issues":
		text = locale.T(lang, "menu.issues")
		keyboard = issuesMenuKeyboard(lang)
	case "notif":
		text = locale.T(lang, "menu.notif")
		keyboard = notifMenuKeyboard(lang)
	case "reports":
		text = locale.T(lang, "menu.reports")
		keyboard = reportsMenuKeyboard(lang)
	case "profile":
		text = locale.T(lang, "menu.profile")
		keyboard = profileMenuKeyboard(lang)
	case "admin":
		if !h.isAdmin(cq.From.ID) {
			return
		}
		text = locale.T(lang, "admin.menu")
		keyboard = adminMenuKeyboard(lang)
	default:
		return
	}

	edit := tgbotapi.NewEditMessageTextAndMarkup(
		cq.Message.Chat.ID, cq.Message.MessageID, text, keyboard)
	edit.ParseMode = tgbotapi.ModeMarkdown
	if _, err := h.api.Send(edit); err != nil {
		msg := tgbotapi.NewMessage(cq.Message.Chat.ID, text)
		msg.ParseMode = tgbotapi.ModeMarkdown
		msg.ReplyMarkup = keyboard
		h.sendMessage(msg)
	}
}

func (h *Handler) handleLangCallback(ctx context.Context, cq *tgbotapi.CallbackQuery, langCode string) {
	lang := locale.FromString(langCode)
	if err := h.userRepo.SetLanguage(ctx, cq.From.ID, string(lang)); err != nil {
		h.log.Error().Err(err).Msg("failed to set language")
	}

	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, locale.T(lang, "lang.changed")))

	editMsg := tgbotapi.NewEditMessageTextAndMarkup(cq.Message.Chat.ID, cq.Message.MessageID,
		locale.T(lang, "lang.changed"),
		menuButtonKeyboard(lang))
	editMsg.ParseMode = tgbotapi.ModeMarkdown
	_, _ = h.api.Send(editMsg)
}

func (h *Handler) handleActionCallback(ctx context.Context, cq *tgbotapi.CallbackQuery, action string) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	chatID := cq.Message.Chat.ID
	userID := cq.From.ID
	lang := h.getLang(ctx, userID)

	switch action {
	case "me":
		h.sendMessage(withMenuButton(h.handleMe(ctx, chatID, userID), lang))
	case "connect":
		h.sendMessage(h.handleConnect(ctx, chatID, userID))
	case "disconnect":
		h.sendMessage(h.handleDisconnect(ctx, chatID, userID))
	case "daily":
		h.sendMessage(withMenuButton(h.handleDaily(ctx, chatID, userID), lang))
	case "daily_user":
		h.states.Set(userID, "daily_search", nil)
		h.sendPrompt(chatID, locale.T(lang, "daily.enter_user"), lang)
	case "issue":
		h.states.Set(userID, "issue", nil)
		h.sendPrompt(chatID, locale.T(lang, "issue.enter_key"), lang)
	case "list":
		h.sendMessage(withMenuButton(h.handleList(ctx, chatID, userID, ""), lang))
	case "filters":
		h.sendMessage(withMenuButton(h.handleFilters(ctx, chatID, userID), lang))
	case "listjql":
		h.states.Set(userID, "list_jql", nil)
		h.sendPrompt(chatID, locale.T(lang, "list.enter_jql"), lang)
	case "comment":
		h.states.Set(userID, "comment_key", nil)
		h.sendPrompt(chatID, locale.T(lang, "comment.enter_key"), lang)
	case "trans":
		h.states.Set(userID, "trans_key", nil)
		h.sendPrompt(chatID, locale.T(lang, "transition.enter_key"), lang)
	case "assign":
		h.states.Set(userID, "assign_key", nil)
		h.sendPrompt(chatID, locale.T(lang, "assign.enter_key"), lang)
	case "subscribe":
		h.handleSubscribeMenu(chatID, lang)
	case "unwatch":
		h.sendMessage(withMenuButton(h.handleUnwatch(ctx, chatID, lang), lang))
	case "subs":
		h.sendMessage(withMenuButton(h.handleSubscriptions(ctx, chatID, lang), lang))
	case "sprint":
		if user, _ := h.userRepo.GetByTelegramID(ctx, userID); user != nil && user.DefaultProject != "" {
			if user.DefaultBoardID != 0 {
				h.sendMessage(h.handleSprintBoard(ctx, chatID, userID, user.DefaultBoardID))
			} else {
				h.sendMessage(h.handleSprintProject(ctx, chatID, userID, user.DefaultProject))
			}
			return
		}
		h.states.Set(userID, "sprint_project", nil)
		h.handleSprintStart(chatID, lang)
	case "sched":
		h.states.Set(userID, "schedule", nil)
		h.sendPrompt(chatID, locale.T(lang, "schedule.enter"), lang)
	case "unsched":
		h.sendMessage(withMenuButton(h.handleUnschedule(ctx, chatID, userID), lang))
	case "scheds":
		h.sendMessage(withMenuButton(h.handleSchedules(ctx, chatID, userID), lang))
	case "defaults":
		h.states.Set(userID, "defaults_project", nil)
		h.sendPrompt(chatID, locale.T(lang, "defaults.enter_project"), lang)
	case "issuetypes":
		h.handleIssueTypesStart(ctx, chatID, userID)
	case "assigneefield":
		h.handleAssigneeFieldStart(ctx, chatID, userID)
	case "spfield":
		h.handleStoryPointsFieldStart(ctx, chatID, userID)
	case "dailyjql":
		h.handleDailyJQLStart(ctx, chatID, userID)
	case "lang":
		h.sendMessage(h.handleLang(chatID, lang))
	case "cancel":
		h.states.Clear(userID)
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "action.cancelled")))
	}
}

// --- Text input (state-based) ---

func (h *Handler) handleTextInput(ctx context.Context, message *tgbotapi.Message) {
	userID := message.From.ID
	chatID := message.Chat.ID
	step, data := h.states.Get(userID)

	text := strings.TrimSpace(message.Text)
	if text == "" {
		return
	}

	if step == "" {
		if match := jiraURLRe.FindStringSubmatch(text); match != nil {
			issueKey := match[1]
			h.handleJiraLink(ctx, message.Chat.ID, userID, issueKey)
		}
		return
	}

	lang := h.getLang(ctx, userID)

	switch step {
	case "issue":
		h.states.Clear(userID)
		if !validateIssueKey(text) {
			h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "issue.invalid_key_short")))
			return
		}
		h.sendMessage(withMenuButton(h.handleIssue(ctx, chatID, userID, text), lang))

	case "list_jql":
		h.states.Clear(userID)
		if len(text) > maxJQLLen {
			h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "list.jql_too_long", maxJQLLen)))
			return
		}
		h.sendMessage(withMenuButton(h.handleList(ctx, chatID, userID, text), lang))

	case "comment_key":
		if !validateIssueKey(text) {
			h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "issue.invalid_key_short")))
			return
		}
		h.states.Set(userID, "comment_text", map[string]string{"issue_key": text})
		h.sendPrompt(chatID, locale.T(lang, "comment.enter_text", text), lang)

	case "comment_text":
		h.states.Clear(userID)
		issueKey := data["issue_key"]
		if len(text) > maxCommentLen {
			h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "comment.too_long", maxCommentLen)))
			return
		}
		h.sendMessage(withMenuButton(h.handleComment(ctx, chatID, userID, issueKey, text), lang))

	case "trans_key":
		h.states.Clear(userID)
		if !validateIssueKey(text) {
			h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "issue.invalid_key_short")))
			return
		}
		h.sendMessage(h.handleTransition(ctx, chatID, userID, text))

	case "assign_key":
		h.states.Clear(userID)
		if !validateIssueKey(text) {
			h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "issue.invalid_key_short")))
			return
		}
		h.sendMessage(withMenuButton(h.handleAssign(ctx, chatID, userID, text), lang))

	case "sub_project":
		h.states.Clear(userID)
		h.handleSubProjectInput(ctx, chatID, userID, text, lang)

	case "sub_issue":
		h.states.Clear(userID)
		h.handleSubIssueInput(ctx, chatID, userID, text, lang)

	case "daily_search":
		h.states.Clear(userID)
		h.sendMessage(withMenuButton(h.handleDailySearch(ctx, chatID, userID, text), lang))

	case "sprint_project":
		h.states.Clear(userID)
		inputParts := strings.SplitN(text, " ", 3)
		projectKey := strings.ToUpper(inputParts[0])
		if !validateProjectKey(projectKey) {
			h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "watch.invalid_project_short")))
			return
		}
		boardHint := ""
		sprintHint := ""
		if len(inputParts) > 1 {
			boardHint = strings.TrimSpace(inputParts[1])
		}
		if len(inputParts) > 2 {
			sprintHint = strings.TrimSpace(inputParts[2])
		}
		h.sendMessage(h.handleSprintFull(ctx, chatID, userID, projectKey, boardHint, sprintHint))

	case "defaults_project":
		h.states.Clear(userID)
		if text == "-" {
			if err := h.userRepo.SetDefaults(ctx, userID, "", 0); err != nil {
				h.log.Error().Err(err).Msg("failed to clear defaults")
			}
			h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "defaults.cleared")))
			return
		}
		projectKey := strings.ToUpper(text)
		if !validateProjectKey(projectKey) {
			h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "watch.invalid_project_short")))
			return
		}
		h.sendMessage(h.handleDefaultsProject(ctx, chatID, userID, projectKey))

	case "defaults_board":
		h.states.Clear(userID)
		projectKey := data["project"]
		h.sendMessage(h.handleDefaultsBoard(ctx, chatID, userID, projectKey, text))

	case "sprint_board":
		h.states.Clear(userID)
		projectKey := data["project"]
		h.sendMessage(h.handleSprintFull(ctx, chatID, userID, projectKey, text, ""))

	case "sprint_sprint":
		h.states.Clear(userID)
		boardID, _ := strconv.Atoi(data["board_id"])
		h.sendMessage(h.handleSprintBoardWithHint(ctx, chatID, userID, boardID, text))

	case "it_project":
		h.states.Clear(userID)
		projectKey := strings.ToUpper(text)
		if !validateProjectKey(projectKey) {
			h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "watch.invalid_project_short")))
			return
		}
		h.showIssueTypePicker(ctx, chatID, userID, projectKey)

	case "daily_jql_done":
		h.states.Clear(userID)
		h.handleDailyJQLSave(ctx, chatID, userID, lang, "done", text)

	case "daily_jql_doing":
		h.states.Clear(userID)
		h.handleDailyJQLSave(ctx, chatID, userID, lang, "doing", text)

	case "daily_jql_plan":
		h.states.Clear(userID)
		h.handleDailyJQLSave(ctx, chatID, userID, lang, "plan", text)

	case "schedule":
		h.states.Clear(userID)
		h.sendMessage(withMenuButton(h.handleSchedule(ctx, chatID, userID, text), lang))

	case "admin_broadcast":
		h.states.Clear(userID)
		if !h.isAdmin(userID) {
			return
		}
		h.handleAdminBroadcast(ctx, chatID, userID, text)
	}
}

// --- Static pages ---

func (h *Handler) handleStart(chatID, userID int64, lang locale.Lang) tgbotapi.MessageConfig {
	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "start.welcome"))
	msg.ParseMode = tgbotapi.ModeMarkdown
	if h.isAdmin(userID) {
		msg.ReplyMarkup = mainMenuKeyboardAdmin(lang)
	} else {
		msg.ReplyMarkup = mainMenuKeyboard(lang)
	}
	return msg
}

func (h *Handler) handleHelp(chatID int64, lang locale.Lang) tgbotapi.MessageConfig {
	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "help.text"))
	msg.ParseMode = tgbotapi.ModeMarkdown
	return msg
}

func (h *Handler) handleLang(chatID int64, lang locale.Lang) tgbotapi.MessageConfig {
	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "lang.choose"))
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("English", "lang:en"),
			tgbotapi.NewInlineKeyboardButtonData("Русский", "lang:ru"),
		),
	)
	return msg
}

// --- Transition callback ---

func (h *Handler) handleTransitionCallback(ctx context.Context, cq *tgbotapi.CallbackQuery, parts []string) {
	callback := tgbotapi.NewCallback(cq.ID, "")
	lang := h.getLang(ctx, cq.From.ID)

	if len(parts) < 3 {
		callback.Text = locale.T(lang, "transition.cb_invalid")
		_, _ = h.api.Request(callback)
		return
	}

	issueKey := parts[1]
	transitionID := parts[2]

	user, err := h.requireAuth(ctx, cq.From.ID)
	if err != nil {
		callback.Text = locale.T(lang, "transition.cb_not_connected")
		_, _ = h.api.Request(callback)
		return
	}

	if err = h.jiraAPI.DoTransition(ctx, user, issueKey, transitionID); err != nil {
		h.log.Error().Err(err).Str("issue", issueKey).Msg("failed to do transition")
		callback.Text = locale.T(lang, "transition.cb_failed")
		_, _ = h.api.Request(callback)
		return
	}

	callback.Text = locale.T(lang, "transition.cb_applied", issueKey)
	_, _ = h.api.Request(callback)

	editMsg := tgbotapi.NewEditMessageTextAndMarkup(cq.Message.Chat.ID, cq.Message.MessageID,
		locale.T(lang, "transition.applied", issueKey),
		menuButtonKeyboard(lang))
	editMsg.ParseMode = tgbotapi.ModeMarkdown
	_, _ = h.api.Send(editMsg)
}

// --- Utility functions ---

func (h *Handler) requireAuth(ctx context.Context, telegramUserID int64) (*storage.User, error) {
	user, err := h.userRepo.GetByTelegramID(ctx, telegramUserID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to get user")
		return nil, fmt.Errorf("something went wrong. Please try again")
	}
	if user == nil || user.AccessToken == "" {
		return nil, fmt.Errorf("you are not connected to Jira. Use /connect first")
	}
	return user, nil
}

// getLang returns the user's preferred language.
func (h *Handler) getLang(ctx context.Context, userID int64) locale.Lang {
	user, err := h.userRepo.GetByTelegramID(ctx, userID)
	if err != nil || user == nil {
		return locale.Default
	}
	return locale.FromString(user.Language)
}

func validateIssueKey(key string) bool {
	return len(key) <= maxIssueKeyLen && issueKeyRe.MatchString(key)
}

func validateProjectKey(key string) bool {
	if key == "" || len(key) > maxProjectKeyLen {
		return false
	}
	for _, c := range key {
		if (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func validateCronExpression(expr string) error {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sched, err := parser.Parse(expr)
	if err != nil {
		return err
	}

	now := time.Now()
	next := sched.Next(now)
	nextAfter := sched.Next(next)
	interval := nextAfter.Sub(next)

	if interval < time.Duration(minCronIntervalMin)*time.Minute {
		return fmt.Errorf("schedule interval too frequent (minimum every %d minutes)", minCronIntervalMin)
	}

	return nil
}

func generateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
