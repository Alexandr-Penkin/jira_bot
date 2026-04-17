package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.mongodb.org/mongo-driver/v2/bson"

	"SleepJiraBot/internal/format"
	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/locale"
	"SleepJiraBot/internal/storage"
)

const (
	maxSummaryLen      = 255
	maxDescriptionLen  = 10000
	maxTemplateNameLen = 50
	maxSelectOptions   = 30
	maxEpicOptions     = 25
)

// --- Entry points ---

// handleCreate handles /create command — either quick mode with args or wizard mode.
func (h *Handler) handleCreate(ctx context.Context, chatID, userID int64, args string) tgbotapi.MessageConfig {
	lang := h.getLang(ctx, userID)
	user, err := h.userRepo.GetByTelegramID(ctx, userID)
	if err != nil || user == nil || user.AccessToken == "" {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected"))
	}

	args = strings.TrimSpace(args)
	if args != "" {
		return h.handleCreateQuick(ctx, chatID, userID, user, args, lang)
	}

	h.handleCreateStart(ctx, chatID, userID)
	return tgbotapi.MessageConfig{}
}

// handleCreateStart begins the wizard flow — shows templates or asks for project.
func (h *Handler) handleCreateStart(ctx context.Context, chatID, userID int64) {
	lang := h.getLang(ctx, userID)
	user, err := h.userRepo.GetByTelegramID(ctx, userID)
	if err != nil || user == nil || user.AccessToken == "" {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected")))
		return
	}

	templates, _ := h.templateRepo.GetByUser(ctx, userID)

	if len(templates) > 0 {
		rows := [][]tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "create.blank"), "cr:blank"),
			),
		}
		for _, tmpl := range templates {
			label := fmt.Sprintf("%s (%s %s)", tmpl.Name, tmpl.ProjectKey, tmpl.IssueTypeName)
			if len(label) > 40 {
				label = label[:37] + "..."
			}
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(label, "cr:tmpl:"+tmpl.ID.Hex()),
			))
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.cancel"), "a:cancel"),
		))

		msg := tgbotapi.NewMessage(chatID, locale.T(lang, "create.choose_template"))
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
		h.sendMessage(msg)
		return
	}

	h.createAskProject(ctx, chatID, userID, user, lang)
}

// createAskProject either uses default project or prompts for one.
func (h *Handler) createAskProject(ctx context.Context, chatID, userID int64, user *storage.User, lang locale.Lang) {
	if user.DefaultProject != "" {
		h.createShowIssueTypes(ctx, chatID, userID, user.DefaultProject, lang)
		return
	}

	h.states.Set(userID, "create_project", nil)
	h.sendPrompt(chatID, locale.T(lang, "create.enter_project"), lang)
}

// --- Text input handlers ---

func (h *Handler) handleCreateProjectInput(ctx context.Context, chatID, userID int64, text string) {
	lang := h.getLang(ctx, userID)
	projectKey := strings.ToUpper(strings.TrimSpace(text))
	if !validateProjectKey(projectKey) {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "watch.invalid_project_short")))
		return
	}
	h.states.Clear(userID)
	h.createShowIssueTypes(ctx, chatID, userID, projectKey, lang)
}

func (h *Handler) handleCreateSummaryInput(ctx context.Context, chatID, userID int64, text string) {
	_, data := h.states.Get(userID)
	lang := h.getLang(ctx, userID)

	if len(text) > maxSummaryLen {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "create.summary_too_long", maxSummaryLen)))
		return
	}

	data["summary"] = text
	h.states.Set(userID, "create_desc_pending", data)

	if defaultDesc, ok := data["desc_template"]; ok && defaultDesc != "" {
		preview := defaultDesc
		if len(preview) > descMaxLen {
			preview = preview[:descMaxLen] + "..."
		}
		msg := tgbotapi.NewMessage(chatID, locale.T(lang, "create.template_prefill", format.EscapeMarkdown(preview)))
		msg.ParseMode = tgbotapi.ModeMarkdownV2
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "create.keep_desc"), "cr:desc:keep"),
				tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "create.edit_desc"), "cr:desc:edit"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.skip"), "cr:desc:skip"),
			),
		)
		h.sendMessage(msg)
		return
	}

	data["description"] = ""
	h.states.Set(userID, "create_description", data)
	h.sendPrompt(chatID, locale.T(lang, "create.enter_description"), lang)
}

func (h *Handler) handleCreateDescriptionInput(ctx context.Context, chatID, userID int64, text string) {
	_, data := h.states.Get(userID)
	lang := h.getLang(ctx, userID)

	if len(text) > maxDescriptionLen {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "create.desc_too_long", maxDescriptionLen)))
		return
	}

	data["description"] = text
	h.states.Set(userID, "create_priority_pending", data)
	h.createShowPriorities(ctx, chatID, userID, data, lang)
}

func (h *Handler) handleCreateAssigneeSearch(ctx context.Context, chatID, userID int64, text string) {
	_, data := h.states.Get(userID)
	lang := h.getLang(ctx, userID)

	user, err := h.userRepo.GetByTelegramID(ctx, userID)
	if err != nil || user == nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic")))
		return
	}

	users, err := h.jiraAPI.SearchUsers(ctx, user, text, 10)
	if err != nil || len(users) == 0 {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "create.no_assignee_found")))
		return
	}

	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(users)+1)
	for _, u := range users {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(u.DisplayName, "cr:asgn_p:"+u.AccountID),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.skip"), "cr:asgn:skip"),
	))

	// Preserve state data while showing results.
	h.states.Set(userID, "create_assignee_pending", data)

	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "create.assignee_results"))
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	h.sendMessage(msg)
}

func (h *Handler) handleCreateCustomFieldInput(ctx context.Context, chatID, userID int64, text string) {
	_, data := h.states.Get(userID)
	lang := h.getLang(ctx, userID)

	fieldID := data["cf_current"]
	fieldType := data["cftype:"+fieldID]

	if fieldType == "number" {
		if _, err := strconv.ParseFloat(text, 64); err != nil {
			h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "create.field_number_invalid")))
			return
		}
	}

	data["cf:"+fieldID] = text
	h.states.Set(userID, "create_cf_next", data)
	h.advanceToNextCustomField(ctx, chatID, userID, data, lang)
}

func (h *Handler) handleCreateEpicKeyInput(ctx context.Context, chatID, userID int64, text string) {
	_, data := h.states.Get(userID)
	lang := h.getLang(ctx, userID)

	key := strings.ToUpper(strings.TrimSpace(text))
	if !validateIssueKey(key) {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "create.epic_key_invalid")))
		return
	}

	data["epic_key"] = key
	if data["epic_retry"] == "1" {
		delete(data, "epic_retry")
		h.handleCreateConfirm(ctx, chatID, userID, data, lang)
		return
	}
	h.states.Set(userID, "create_cf_pending", data)
	h.startCustomFields(ctx, chatID, userID, data, lang)
}

func (h *Handler) handleCreateTemplateNameInput(ctx context.Context, chatID, userID int64, text string) {
	_, data := h.states.Get(userID)
	lang := h.getLang(ctx, userID)

	if len(text) > maxTemplateNameLen {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "create.tmpl_name_too_long", maxTemplateNameLen)))
		return
	}

	count, _ := h.templateRepo.CountByUser(ctx, userID)
	if count >= int64(storage.MaxTemplatesPerUser) {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "create.tmpl_limit_reached", storage.MaxTemplatesPerUser)))
		return
	}

	tmpl := &storage.IssueTemplate{
		TelegramUserID: userID,
		Name:           text,
		ProjectKey:     data["project"],
		IssueTypeID:    data["issue_type_id"],
		IssueTypeName:  data["issue_type_name"],
		Fields:         buildTemplateFields(data),
	}

	if err := h.templateRepo.Create(ctx, tmpl); err != nil {
		h.log.Error().Err(err).Msg("failed to save template")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic")))
		return
	}

	h.states.Clear(userID)
	h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "create.tmpl_saved", text)))
}

// --- Wizard step renderers ---

func (h *Handler) createShowIssueTypes(ctx context.Context, chatID, userID int64, projectKey string, lang locale.Lang) {
	user, err := h.userRepo.GetByTelegramID(ctx, userID)
	if err != nil || user == nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic")))
		return
	}

	issueTypes, err := h.jiraAPI.GetCreateMetaIssueTypes(ctx, user, projectKey)
	if err != nil {
		h.log.Error().Err(err).Str("project", projectKey).Msg("failed to get create meta issue types")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "create.failed_types")))
		return
	}

	if len(issueTypes) == 0 {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "create.no_types")))
		return
	}

	data := map[string]string{"project": projectKey}
	h.states.Set(userID, "create_type_pending", data)

	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(issueTypes)+1)
	for _, it := range issueTypes {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(it.Name, "cr:type:"+it.ID+":"+it.Name),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.cancel"), "cr:cancel"),
	))

	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "create.choose_type"))
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	h.sendMessage(msg)
}

func (h *Handler) createShowPriorities(ctx context.Context, chatID, userID int64, data map[string]string, lang locale.Lang) {
	user, err := h.userRepo.GetByTelegramID(ctx, userID)
	if err != nil || user == nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic")))
		return
	}

	priorities, err := h.jiraAPI.GetPriorities(ctx, user)
	if err != nil || len(priorities) == 0 {
		// Priority not available — skip to assignee.
		h.states.Set(userID, "create_assignee_pending", data)
		h.createShowAssigneeOptions(chatID, userID, data, lang)
		return
	}

	h.states.Set(userID, "create_priority_pending", data)

	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(priorities)+1)
	for _, p := range priorities {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(p.Name, "cr:pri:"+p.ID+":"+p.Name),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.skip"), "cr:pri:skip"),
	))

	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "create.choose_priority"))
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	h.sendMessage(msg)
}

func (h *Handler) createShowAssigneeOptions(chatID, userID int64, data map[string]string, lang locale.Lang) {
	h.states.Set(userID, "create_assignee_pending", data)

	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "create.choose_assignee"))
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "create.assign_me"), "cr:asgn:me"),
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "create.search_user"), "cr:asgn:search"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.skip"), "cr:asgn:skip"),
		),
	)
	h.sendMessage(msg)
}

// createMaybeAskEpic routes to the Epic picker step if Epic is required, otherwise skips to custom fields.
func (h *Handler) createMaybeAskEpic(ctx context.Context, chatID, userID int64, data map[string]string, lang locale.Lang) {
	if data["epic_required"] != "1" {
		h.states.Set(userID, "create_cf_pending", data)
		h.startCustomFields(ctx, chatID, userID, data, lang)
		return
	}
	h.createShowEpicOptions(ctx, chatID, userID, data, lang)
}

// createShowEpicOptions fetches active Epics in the current project and presents them as buttons.
func (h *Handler) createShowEpicOptions(ctx context.Context, chatID, userID int64, data map[string]string, lang locale.Lang) {
	user, err := h.userRepo.GetByTelegramID(ctx, userID)
	if err != nil || user == nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic")))
		return
	}

	jql := fmt.Sprintf(`project = "%s" AND issuetype = Epic AND statusCategory != Done ORDER BY updated DESC`, data["project"])
	result, err := h.jiraAPI.SearchIssues(ctx, user, jql, maxEpicOptions)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to load epics")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "create.failed_epics")))
		// Fall back to manual key entry.
		h.states.Set(userID, "create_epic_key", data)
		h.sendPrompt(chatID, locale.T(lang, "create.enter_epic_key"), lang)
		return
	}

	// Clear any stale epic summary mappings from previous runs.
	for k := range data {
		if strings.HasPrefix(k, "epic_sum:") {
			delete(data, k)
		}
	}

	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(result.Issues)+2)
	for _, issue := range result.Issues {
		label := fmt.Sprintf("%s — %s", issue.Key, issue.Fields.Summary)
		if len(label) > 50 {
			label = label[:47] + "..."
		}
		data["epic_sum:"+issue.Key] = issue.Fields.Summary
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, "cr:epic:"+issue.Key),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "create.enter_epic_manual"), "cr:epic_manual"),
	))
	if data["epic_required"] != "1" {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.skip"), "cr:epic:skip"),
		))
	}

	h.states.Set(userID, "create_epic_pending", data)

	prompt := "create.choose_epic"
	if len(result.Issues) == 0 {
		prompt = "create.no_epics"
	}
	msg := tgbotapi.NewMessage(chatID, locale.T(lang, prompt))
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	h.sendMessage(msg)
}

func (h *Handler) createShowConfirmation(chatID, userID int64, data map[string]string, lang locale.Lang) {
	h.states.Set(userID, "create_confirm_pending", data)

	text := h.buildCreateConfirmation(data, lang)
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdownV2
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "create.confirm"), "cr:confirm"),
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "create.save_tmpl"), "cr:save_tmpl"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.cancel"), "cr:cancel"),
		),
	)
	h.sendMessage(msg)
}

// --- Callback handler ---

func (h *Handler) handleCreateCallback(ctx context.Context, cq *tgbotapi.CallbackQuery, parts []string) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	chatID := cq.Message.Chat.ID
	userID := cq.From.ID
	lang := h.getLang(ctx, userID)

	if len(parts) < 2 {
		return
	}

	action := parts[1]
	_, data := h.states.Get(userID)
	if data == nil {
		data = make(map[string]string)
	}

	switch action {
	case "blank":
		user, err := h.userRepo.GetByTelegramID(ctx, userID)
		if err != nil || user == nil {
			h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic")))
			return
		}
		h.createAskProject(ctx, chatID, userID, user, lang)

	case "tmpl":
		if len(parts) < 3 {
			return
		}
		h.handleCreateFromTemplate(ctx, chatID, userID, parts[2], lang)

	case "type":
		if len(parts) < 4 {
			return
		}
		issueTypeID := parts[2]
		issueTypeName := parts[3]
		data["issue_type_id"] = issueTypeID
		data["issue_type_name"] = issueTypeName

		h.createFetchFieldsAndAskSummary(ctx, chatID, userID, data, lang)

	case "desc":
		if len(parts) < 3 {
			return
		}
		switch parts[2] {
		case "keep":
			data["description"] = data["desc_template"]
			data["desc_is_adf"] = "true"
			h.states.Set(userID, "create_priority_pending", data)
			h.createShowPriorities(ctx, chatID, userID, data, lang)
		case "edit":
			h.states.Set(userID, "create_description", data)
			h.sendPrompt(chatID, locale.T(lang, "create.enter_description"), lang)
		case "skip":
			data["description"] = ""
			h.states.Set(userID, "create_priority_pending", data)
			h.createShowPriorities(ctx, chatID, userID, data, lang)
		}

	case "pri":
		if len(parts) < 3 {
			return
		}
		if parts[2] != "skip" {
			data["priority_id"] = parts[2]
			if len(parts) >= 4 {
				data["priority_name"] = parts[3]
			}
		}
		h.states.Set(userID, "create_assignee_pending", data)
		h.createShowAssigneeOptions(chatID, userID, data, lang)

	case "asgn":
		if len(parts) < 3 {
			return
		}
		switch parts[2] {
		case "me":
			user, err := h.userRepo.GetByTelegramID(ctx, userID)
			if err != nil || user == nil {
				h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic")))
				return
			}
			data["assignee_id"] = user.JiraAccountID
			data["assignee_name"] = user.JiraDisplayName
			h.createMaybeAskEpic(ctx, chatID, userID, data, lang)
		case "search":
			h.states.Set(userID, "create_assignee_search", data)
			h.sendPrompt(chatID, locale.T(lang, "create.search_assignee"), lang)
		case "skip":
			h.createMaybeAskEpic(ctx, chatID, userID, data, lang)
		}

	case "asgn_p":
		if len(parts) < 3 {
			return
		}
		accountID := parts[2]
		data["assignee_id"] = accountID
		// Try to resolve display name.
		user, _ := h.userRepo.GetByTelegramID(ctx, userID)
		if user != nil {
			users, _ := h.jiraAPI.SearchUsers(ctx, user, accountID, 1)
			for _, u := range users {
				if u.AccountID == accountID {
					data["assignee_name"] = u.DisplayName
					break
				}
			}
		}
		h.createMaybeAskEpic(ctx, chatID, userID, data, lang)

	case "epic":
		if len(parts) < 3 {
			return
		}
		if parts[2] == "skip" {
			// Skip only allowed if epic not required; enforced by button visibility.
			h.states.Set(userID, "create_cf_pending", data)
			h.startCustomFields(ctx, chatID, userID, data, lang)
			return
		}
		epicKey := parts[2]
		data["epic_key"] = epicKey
		if summary := data["epic_sum:"+epicKey]; summary != "" {
			data["epic_summary"] = summary
		}
		if data["epic_retry"] == "1" {
			delete(data, "epic_retry")
			h.handleCreateConfirm(ctx, chatID, userID, data, lang)
			return
		}
		h.states.Set(userID, "create_cf_pending", data)
		h.startCustomFields(ctx, chatID, userID, data, lang)

	case "epic_manual":
		h.states.Set(userID, "create_epic_key", data)
		h.sendPrompt(chatID, locale.T(lang, "create.enter_epic_key"), lang)

	case "cf":
		if len(parts) < 4 {
			return
		}
		fieldID := parts[2]
		valueID := parts[3]
		// For select fields, store value and name.
		data["cf:"+fieldID] = valueID
		if len(parts) >= 5 {
			data["cfval:"+fieldID] = parts[4]
		}
		h.states.Set(userID, "create_cf_next", data)
		h.advanceToNextCustomField(ctx, chatID, userID, data, lang)

	case "cf_skip":
		if len(parts) < 3 {
			return
		}
		h.states.Set(userID, "create_cf_next", data)
		h.advanceToNextCustomField(ctx, chatID, userID, data, lang)

	case "confirm":
		h.handleCreateConfirm(ctx, chatID, userID, data, lang)

	case "save_tmpl":
		h.states.Set(userID, "create_template_name", data)
		h.sendPrompt(chatID, locale.T(lang, "create.enter_tmpl_name"), lang)

	case "cancel":
		h.states.Clear(userID)
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "action.cancelled")))

	case "tmpl_del":
		if len(parts) < 3 {
			return
		}
		h.handleDeleteTemplate(ctx, chatID, userID, parts[2], lang)
	}
}

// --- Core wizard logic ---

func (h *Handler) createFetchFieldsAndAskSummary(ctx context.Context, chatID, userID int64, data map[string]string, lang locale.Lang) {
	user, err := h.userRepo.GetByTelegramID(ctx, userID)
	if err != nil || user == nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic")))
		return
	}

	fields, err := h.jiraAPI.GetCreateMetaFields(ctx, user, data["project"], data["issue_type_id"])
	if err != nil {
		h.log.Error().Err(err).Msg("failed to get create meta fields")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "create.failed_fields")))
		return
	}

	// Extract description template and detect required Epic/parent field if present.
	for i := range fields {
		f := &fields[i]
		if f.Key == "description" && f.HasDefaultValue && len(f.DefaultValue) > 0 {
			var adfDoc jira.ADFDocument
			if json.Unmarshal(f.DefaultValue, &adfDoc) == nil {
				extracted := adfDoc.ExtractText()
				if strings.TrimSpace(extracted) != "" {
					data["desc_template"] = extracted
					data["desc_template_raw"] = string(f.DefaultValue)
				}
			}
		}
		// Record Epic-capable field id regardless of required flag, so we can
		// auto-retry when a workflow validator (not createmeta) demands one.
		if f.Key == "parent" && !strings.EqualFold(data["issue_type_name"], "Epic") {
			if data["epic_field_id"] == "" {
				data["epic_field_id"] = "parent"
			}
			if f.Required {
				data["epic_required"] = "1"
			}
		}
		if f.Schema.Custom == "com.pyxis.greenhopper.jira:gh-epic-link" {
			data["epic_field_id"] = f.FieldID
			if f.Required {
				data["epic_required"] = "1"
			}
		}
	}

	// Store custom fields metadata in state.
	customFields := filterSupportedCustomFields(fields)
	if len(customFields) > 0 {
		var cfOrder []string
		for i := range customFields {
			cf := &customFields[i]
			cfOrder = append(cfOrder, cf.FieldID)
			data["cfname:"+cf.FieldID] = cf.Name
			data["cftype:"+cf.FieldID] = cf.Schema.Type
			if cf.Required {
				data["cfreq:"+cf.FieldID] = "1"
			}
			if len(cf.AllowedValues) > 0 {
				avJSON, _ := json.Marshal(cf.AllowedValues)
				data["cfav:"+cf.FieldID] = string(avJSON)
			}
		}
		data["cf_order"] = strings.Join(cfOrder, ",")
		data["cf_idx"] = "0"
	}

	h.states.Set(userID, "create_summary", data)
	h.sendPrompt(chatID, locale.T(lang, "create.enter_summary"), lang)
}

func (h *Handler) handleCreateConfirm(ctx context.Context, chatID, userID int64, data map[string]string, lang locale.Lang) {
	user, err := h.userRepo.GetByTelegramID(ctx, userID)
	if err != nil || user == nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic")))
		return
	}

	payload := buildCreatePayload(data)

	resp, err := h.jiraAPI.CreateIssue(ctx, user, payload)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create issue")
		detail := extractJiraErrorDetail(err)
		if detail != "" && data["epic_key"] == "" && isEpicRequiredError(detail) {
			// Workflow validator demands an Epic even though createmeta didn't
			// flag it. Mark required, show picker, retry after selection.
			data["epic_required"] = "1"
			data["epic_retry"] = "1"
			if data["epic_field_id"] == "" {
				data["epic_field_id"] = "parent"
			}
			h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "create.epic_required_retry")))
			h.createShowEpicOptions(ctx, chatID, userID, data, lang)
			return
		}
		if detail != "" {
			h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "create.failed_detail", detail)))
		} else {
			h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "create.failed")))
		}
		return
	}

	h.states.Clear(userID)

	issueURL := fmt.Sprintf("%s/browse/%s", user.JiraSiteURL, resp.Key)
	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "create.success", resp.Key, issueURL))
	msg.ParseMode = tgbotapi.ModeMarkdownV2
	msg.DisableWebPagePreview = true
	h.sendMessage(msg)
}

// isEpicRequiredError reports whether a Jira error message indicates the
// missing field is an Epic (validator- or field-enforced).
func isEpicRequiredError(detail string) bool {
	d := strings.ToLower(detail)
	return strings.Contains(d, "epic") && strings.Contains(d, "required")
}

// extractJiraErrorDetail returns a human-readable summary from a Jira 4xx error
// body (errorMessages + errors map) — empty if the error isn't an HTTPError.
func extractJiraErrorDetail(err error) string {
	var httpErr *jira.HTTPError
	if !errors.As(err, &httpErr) {
		return ""
	}
	var body struct {
		ErrorMessages []string          `json:"errorMessages"`
		Errors        map[string]string `json:"errors"`
	}
	if jsonErr := json.Unmarshal([]byte(httpErr.Body), &body); jsonErr != nil {
		return ""
	}
	parts := append([]string(nil), body.ErrorMessages...)
	for field, msg := range body.Errors {
		parts = append(parts, fmt.Sprintf("%s: %s", field, msg))
	}
	return strings.Join(parts, "; ")
}

// --- Custom field iteration ---

func (h *Handler) startCustomFields(ctx context.Context, chatID, userID int64, data map[string]string, lang locale.Lang) {
	cfOrder := data["cf_order"]
	if cfOrder == "" {
		h.createShowConfirmation(chatID, userID, data, lang)
		return
	}

	data["cf_idx"] = "0"
	h.states.Set(userID, "create_cf_pending", data)
	h.advanceToNextCustomField(ctx, chatID, userID, data, lang)
}

func (h *Handler) advanceToNextCustomField(_ context.Context, chatID, userID int64, data map[string]string, lang locale.Lang) {
	cfOrder := strings.Split(data["cf_order"], ",")
	idx, _ := strconv.Atoi(data["cf_idx"])

	// Find next unfilled field starting from idx.
	for idx < len(cfOrder) {
		fieldID := cfOrder[idx]
		if _, filled := data["cf:"+fieldID]; filled {
			idx++
			continue
		}
		break
	}

	if idx >= len(cfOrder) {
		h.createShowConfirmation(chatID, userID, data, lang)
		return
	}

	fieldID := cfOrder[idx]
	data["cf_idx"] = strconv.Itoa(idx + 1)
	data["cf_current"] = fieldID
	fieldName := data["cfname:"+fieldID]
	fieldType := data["cftype:"+fieldID]
	required := data["cfreq:"+fieldID] == "1"

	// Check if it has allowed values (select field).
	if avJSON, ok := data["cfav:"+fieldID]; ok && avJSON != "" {
		var allowedValues []jira.CreateMetaValue
		if json.Unmarshal([]byte(avJSON), &allowedValues) == nil && len(allowedValues) > 0 {
			h.states.Set(userID, "create_cf_select", data)
			h.showCustomFieldSelect(chatID, fieldID, fieldName, allowedValues, required, lang)
			return
		}
	}

	// Text/number input.
	h.states.Set(userID, "create_custom_field", data)
	prompt := locale.T(lang, "create.enter_field", fieldName)
	if fieldType == "number" {
		prompt = locale.T(lang, "create.enter_field_number", fieldName)
	}

	if required {
		h.sendPrompt(chatID, prompt, lang)
	} else {
		msg := tgbotapi.NewMessage(chatID, prompt)
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.skip"), "cr:cf_skip:"+fieldID),
			),
		)
		h.sendMessage(msg)
	}
}

func (h *Handler) showCustomFieldSelect(chatID int64, fieldID, fieldName string, values []jira.CreateMetaValue, required bool, lang locale.Lang) {
	if len(values) > maxSelectOptions {
		values = values[:maxSelectOptions]
	}

	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(values)+1)
	for _, v := range values {
		displayName := v.Name
		if displayName == "" {
			displayName = v.Value
		}
		// Callback: cr:cf:{fieldID}:{valueID}:{displayName}
		cbData := fmt.Sprintf("cr:cf:%s:%s", fieldID, v.ID)
		// Truncate if too long for callback (64 bytes max).
		if len(cbData) > 60 {
			cbData = cbData[:60]
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(displayName, cbData),
		))
	}
	if !required {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.skip"), "cr:cf_skip:"+fieldID),
		))
	}

	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "create.choose_field", fieldName))
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	h.sendMessage(msg)
}

// --- Template operations ---

func (h *Handler) handleCreateFromTemplate(ctx context.Context, chatID, userID int64, templateIDHex string, lang locale.Lang) {
	oid, err := bson.ObjectIDFromHex(templateIDHex)
	if err != nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic")))
		return
	}

	tmpl, err := h.templateRepo.GetByID(ctx, oid)
	if err != nil || tmpl == nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic")))
		return
	}

	data := map[string]string{
		"project":         tmpl.ProjectKey,
		"issue_type_id":   tmpl.IssueTypeID,
		"issue_type_name": tmpl.IssueTypeName,
	}
	for k, v := range tmpl.Fields {
		data[k] = v
	}

	// Fetch fields metadata and go straight to summary (or skip pre-filled steps).
	h.createFetchFieldsAndAskSummary(ctx, chatID, userID, data, lang)
}

func (h *Handler) handleTemplatesList(ctx context.Context, chatID, userID int64) {
	lang := h.getLang(ctx, userID)
	templates, err := h.templateRepo.GetByUser(ctx, userID)
	if err != nil || len(templates) == 0 {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "create.tmpl_none")))
		return
	}

	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(templates)+1)
	for _, tmpl := range templates {
		label := fmt.Sprintf("%s (%s)", tmpl.Name, tmpl.ProjectKey)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("X "+label, "cr:tmpl_del:"+tmpl.ID.Hex()),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.back"), "m:main"),
	))

	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "create.tmpl_list"))
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	h.sendMessage(msg)
}

func (h *Handler) handleDeleteTemplate(ctx context.Context, chatID, _ int64, templateIDHex string, lang locale.Lang) {
	oid, err := bson.ObjectIDFromHex(templateIDHex)
	if err != nil {
		return
	}

	if err = h.templateRepo.Delete(ctx, oid); err != nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic")))
		return
	}

	h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "create.tmpl_deleted")))
}

// --- Quick create ---

func (h *Handler) handleCreateQuick(ctx context.Context, chatID, _ int64, user *storage.User, args string, lang locale.Lang) tgbotapi.MessageConfig {
	// Format: /create PROJ TypeName | Summary | Description
	argParts := strings.SplitN(args, " ", 2)
	if len(argParts) < 2 {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "create.usage"))
	}

	projectKey := strings.ToUpper(argParts[0])
	if !validateProjectKey(projectKey) {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "create.usage"))
	}

	rest := argParts[1]
	pipeParts := strings.SplitN(rest, "|", 3)
	if len(pipeParts) < 2 {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "create.usage"))
	}

	typeName := strings.TrimSpace(pipeParts[0])
	summary := strings.TrimSpace(pipeParts[1])
	description := ""
	if len(pipeParts) >= 3 {
		description = strings.TrimSpace(pipeParts[2])
	}

	if summary == "" || typeName == "" {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "create.usage"))
	}

	// Resolve issue type ID.
	issueTypes, err := h.jiraAPI.GetCreateMetaIssueTypes(ctx, user, projectKey)
	if err != nil {
		h.log.Error().Err(err).Msg("quick create: failed to get issue types")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "create.failed"))
	}

	var issueTypeID string
	for _, it := range issueTypes {
		if strings.EqualFold(it.Name, typeName) {
			issueTypeID = it.ID
			break
		}
	}
	if issueTypeID == "" {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "create.unknown_type", typeName))
	}

	fields := map[string]interface{}{
		"project":   map[string]string{"key": projectKey},
		"issuetype": map[string]string{"id": issueTypeID},
		"summary":   summary,
	}
	if description != "" {
		fields["description"] = buildADFFromText(description)
	}

	resp, err := h.jiraAPI.CreateIssue(ctx, user, fields)
	if err != nil {
		h.log.Error().Err(err).Msg("quick create: failed to create issue")
		if detail := extractJiraErrorDetail(err); detail != "" {
			return tgbotapi.NewMessage(chatID, locale.T(lang, "create.failed_detail", detail))
		}
		return tgbotapi.NewMessage(chatID, locale.T(lang, "create.failed"))
	}

	issueURL := fmt.Sprintf("%s/browse/%s", user.JiraSiteURL, resp.Key)
	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "create.success", format.EscapeMarkdown(resp.Key), format.EscapeMarkdown(issueURL)))
	msg.ParseMode = tgbotapi.ModeMarkdownV2
	msg.DisableWebPagePreview = true
	return msg
}

// --- Helpers ---

func buildCreatePayload(data map[string]string) map[string]interface{} {
	fields := map[string]interface{}{
		"project":   map[string]string{"key": data["project"]},
		"issuetype": map[string]string{"id": data["issue_type_id"]},
		"summary":   data["summary"],
	}

	if desc := data["description"]; desc != "" {
		if data["desc_is_adf"] == "true" {
			if raw, ok := data["desc_template_raw"]; ok && raw != "" {
				var adf interface{}
				if json.Unmarshal([]byte(raw), &adf) == nil {
					fields["description"] = adf
				}
			}
		} else {
			fields["description"] = buildADFFromText(desc)
		}
	}

	if priID := data["priority_id"]; priID != "" {
		fields["priority"] = map[string]string{"id": priID}
	}

	if assigneeID := data["assignee_id"]; assigneeID != "" {
		fields["assignee"] = map[string]string{"accountId": assigneeID}
	}

	if epicKey := data["epic_key"]; epicKey != "" {
		switch fieldID := data["epic_field_id"]; fieldID {
		case "parent", "":
			fields["parent"] = map[string]string{"key": epicKey}
		default:
			fields[fieldID] = epicKey
		}
	}

	// Custom fields.
	for k, v := range data {
		if !strings.HasPrefix(k, "cf:") {
			continue
		}
		fieldID := strings.TrimPrefix(k, "cf:")
		fieldType := data["cftype:"+fieldID]

		switch fieldType {
		case "number":
			if num, err := strconv.ParseFloat(v, 64); err == nil {
				fields[fieldID] = num
			}
		case "option":
			fields[fieldID] = map[string]string{"id": v}
		case "array":
			fields[fieldID] = []map[string]string{{"id": v}}
		default:
			fields[fieldID] = v
		}
	}

	return fields
}

func buildADFFromText(text string) map[string]interface{} {
	return map[string]interface{}{
		"type":    "doc",
		"version": 1,
		"content": []map[string]interface{}{
			{
				"type": "paragraph",
				"content": []map[string]interface{}{
					{
						"type": "text",
						"text": text,
					},
				},
			},
		},
	}
}

func (h *Handler) buildCreateConfirmation(data map[string]string, lang locale.Lang) string {
	var sb strings.Builder
	sb.WriteString(format.EscapeMarkdown(locale.T(lang, "create.confirm_title")))
	sb.WriteString("\n\n")

	sb.WriteString("*" + format.EscapeMarkdown(locale.T(lang, "create.field_project")) + ":* ")
	sb.WriteString(format.EscapeMarkdown(data["project"]))
	sb.WriteString("\n")

	sb.WriteString("*" + format.EscapeMarkdown(locale.T(lang, "create.field_type")) + ":* ")
	sb.WriteString(format.EscapeMarkdown(data["issue_type_name"]))
	sb.WriteString("\n")

	sb.WriteString("*" + format.EscapeMarkdown(locale.T(lang, "create.field_summary")) + ":* ")
	sb.WriteString(format.EscapeMarkdown(data["summary"]))
	sb.WriteString("\n")

	if desc := data["description"]; desc != "" {
		preview := desc
		if len(preview) > 100 {
			preview = preview[:97] + "..."
		}
		sb.WriteString("*" + format.EscapeMarkdown(locale.T(lang, "create.field_description")) + ":* ")
		sb.WriteString(format.EscapeMarkdown(preview))
		sb.WriteString("\n")
	}

	if name := data["priority_name"]; name != "" {
		sb.WriteString("*" + format.EscapeMarkdown(locale.T(lang, "create.field_priority")) + ":* ")
		sb.WriteString(format.EscapeMarkdown(name))
		sb.WriteString("\n")
	}

	if name := data["assignee_name"]; name != "" {
		sb.WriteString("*" + format.EscapeMarkdown(locale.T(lang, "create.field_assignee")) + ":* ")
		sb.WriteString(format.EscapeMarkdown(name))
		sb.WriteString("\n")
	}

	if epicKey := data["epic_key"]; epicKey != "" {
		sb.WriteString("*" + format.EscapeMarkdown(locale.T(lang, "create.field_epic")) + ":* ")
		sb.WriteString(format.EscapeMarkdown(epicKey))
		if summary := data["epic_summary"]; summary != "" {
			sb.WriteString(" — ")
			sb.WriteString(format.EscapeMarkdown(summary))
		}
		sb.WriteString("\n")
	}

	// Custom fields.
	for k, v := range data {
		if !strings.HasPrefix(k, "cf:") {
			continue
		}
		fieldID := strings.TrimPrefix(k, "cf:")
		fieldName := data["cfname:"+fieldID]
		if fieldName == "" {
			fieldName = fieldID
		}
		displayVal := v
		if valName, ok := data["cfval:"+fieldID]; ok && valName != "" {
			displayVal = valName
		}
		sb.WriteString("*" + format.EscapeMarkdown(fieldName) + ":* ")
		sb.WriteString(format.EscapeMarkdown(displayVal))
		sb.WriteString("\n")
	}

	return sb.String()
}

func buildTemplateFields(data map[string]string) map[string]string {
	fields := make(map[string]string)
	fieldsToSave := []string{"priority_id", "priority_name", "assignee_id", "assignee_name", "description"}
	for _, k := range fieldsToSave {
		if v, ok := data[k]; ok && v != "" {
			fields[k] = v
		}
	}
	for k, v := range data {
		if strings.HasPrefix(k, "cf:") || strings.HasPrefix(k, "cfval:") {
			fields[k] = v
		}
	}
	return fields
}

func filterSupportedCustomFields(fields []jira.CreateMetaField) []jira.CreateMetaField {
	skipKeys := map[string]bool{
		"project": true, "issuetype": true, "summary": true,
		"description": true, "priority": true, "assignee": true,
		"reporter": true, "labels": true, "attachment": true,
		"issuelinks": true, "parent": true,
	}

	supportedTypes := map[string]bool{
		"string": true, "number": true, "option": true, "array": true,
	}

	var result []jira.CreateMetaField
	for i := range fields {
		f := &fields[i]
		if skipKeys[f.Key] {
			continue
		}
		if f.Schema.System != "" {
			continue
		}
		if !supportedTypes[f.Schema.Type] {
			continue
		}
		// For array type, only support option items.
		if f.Schema.Type == "array" && len(f.AllowedValues) == 0 {
			continue
		}
		result = append(result, *f)
	}

	return result
}
