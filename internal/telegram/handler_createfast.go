package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"SleepJiraBot/internal/format"
	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/locale"
	"SleepJiraBot/internal/storage"
)

const (
	createFastCommand         = "createfast"
	createFastEpicPending     = "createfast_epic_pending"
	createFastEpicCallback    = "cf_epic"
	createFastConfirmPending  = "createfast_confirm_pending"
	createFastConfirmCallback = "cf_cfm"
)

// createFastFile carries everything the handler needs to upload a single
// attachment to Jira. Populated by extractCreateFastFiles.
type createFastFile struct {
	FileID      string
	FileName    string
	ContentType string
}

// captionCommand returns the leading bot_command in a photo/document
// caption plus the remaining text. Empty strings when the message has no
// caption command.
func captionCommand(msg *tgbotapi.Message) (cmd, args string) {
	if msg == nil || msg.Caption == "" || len(msg.CaptionEntities) == 0 {
		return "", ""
	}
	first := msg.CaptionEntities[0]
	if first.Type != "bot_command" || first.Offset != 0 {
		return "", ""
	}
	cmdRaw := msg.Caption[:first.Length]
	cmd = strings.TrimPrefix(cmdRaw, "/")
	if at := strings.Index(cmd, "@"); at != -1 {
		cmd = cmd[:at]
	}
	args = strings.TrimSpace(msg.Caption[first.Length:])
	return cmd, args
}

// handleCreateFastText handles the plain-text /createfast command.
func (h *Handler) handleCreateFastText(ctx context.Context, msg *tgbotapi.Message) {
	args := strings.TrimSpace(msg.CommandArguments())
	h.createFast(ctx, msg.Chat.ID, msg.From.ID, args, nil)
}

// handleCreateFastMedia handles /createfast when it's the caption on a
// photo or image document.
func (h *Handler) handleCreateFastMedia(ctx context.Context, msg *tgbotapi.Message, args string) {
	lang := h.getLang(ctx, msg.From.ID)

	files, err := extractCreateFastFiles(msg)
	if err != nil {
		h.sendMessage(tgbotapi.NewMessage(msg.Chat.ID, locale.T(lang, "createfast.unsupported_file")))
		return
	}

	h.createFast(ctx, msg.Chat.ID, msg.From.ID, args, files)
}

// createFast is the shared core: validate defaults, create the issue, and
// (optionally) upload attachments.
func (h *Handler) createFast(ctx context.Context, chatID, userID int64, args string, files []createFastFile) {
	lang := h.getLang(ctx, userID)

	user, err := h.userRepo.GetByTelegramID(ctx, userID)
	if err != nil || user == nil || user.AccessToken == "" {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected")))
		return
	}

	if user.DefaultProject == "" {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "createfast.no_defaults")))
		return
	}

	if user.DefaultIssueTypeID == "" {
		typeID, typeName, resolveErr := h.resolveDefaultIssueType(ctx, user)
		if resolveErr != nil || typeID == "" {
			if resolveErr != nil {
				h.log.Warn().Err(resolveErr).Str("project", user.DefaultProject).Msg("createfast: could not auto-resolve issue type")
			}
			h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "createfast.no_defaults")))
			return
		}
		if saveErr := h.prefs.SetDefaultIssueType(ctx, userID, typeID, typeName); saveErr != nil {
			h.log.Error().Err(saveErr).Msg("createfast: failed to persist auto-resolved issue type")
		}
		user.DefaultIssueTypeID = typeID
		user.DefaultIssueTypeName = typeName
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "createfast.auto_issue_type", typeName)))
	}

	summary, description := splitCreateFastArgs(args)
	if summary == "" {
		if len(files) > 0 {
			summary = "Screenshot " + time.Now().Format("2006-01-02 15:04")
		} else {
			h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "createfast.summary_empty")))
			return
		}
	}
	summary = format.TruncateRunes(summary, maxSummaryLen)

	fields := map[string]interface{}{
		"project":   map[string]string{"key": user.DefaultProject},
		"issuetype": map[string]string{"id": user.DefaultIssueTypeID},
		"summary":   summary,
	}
	if description != "" {
		fields["description"] = buildADFFromText(description)
	}

	epicFieldID, _ := h.fillRequiredDefaults(ctx, user, fields)

	h.showCreateFastConfirmation(ctx, chatID, userID, user, fields, files, epicFieldID, summary, description, lang)
}

// showCreateFastConfirmation renders a preview of the about-to-be-created
// issue and asks the user to confirm. Nothing hits Jira until the user taps
// the Create button.
func (h *Handler) showCreateFastConfirmation(ctx context.Context, chatID, userID int64, user *storage.User, fields map[string]interface{}, files []createFastFile, epicFieldID, summary, description string, lang locale.Lang) {
	payloadJSON, err := json.Marshal(fields)
	if err != nil {
		h.log.Error().Err(err).Msg("createfast: marshal payload for confirmation failed")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "create.failed")))
		return
	}
	filesJSON, err := json.Marshal(files)
	if err != nil {
		h.log.Error().Err(err).Msg("createfast: marshal files for confirmation failed")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "create.failed")))
		return
	}

	h.states.Set(userID, createFastConfirmPending, map[string]string{
		"payload":       string(payloadJSON),
		"files":         string(filesJSON),
		"epic_field_id": epicFieldID,
		"summary":       summary,
		"description":   description,
	})

	typeLabel := user.DefaultIssueTypeName
	if typeLabel == "" {
		typeLabel = user.DefaultIssueTypeID
	}

	var sb strings.Builder
	sb.WriteString(format.EscapeMarkdownV2(locale.T(lang, "createfast.confirm_title")))
	sb.WriteString("\n\n")
	sb.WriteString("*")
	sb.WriteString(format.EscapeMarkdownV2(locale.T(lang, "createfast.confirm_project")))
	sb.WriteString(":* ")
	sb.WriteString(format.EscapeMarkdownV2(user.DefaultProject))
	sb.WriteString("\n*")
	sb.WriteString(format.EscapeMarkdownV2(locale.T(lang, "createfast.confirm_type")))
	sb.WriteString(":* ")
	sb.WriteString(format.EscapeMarkdownV2(typeLabel))
	sb.WriteString("\n*")
	sb.WriteString(format.EscapeMarkdownV2(locale.T(lang, "createfast.confirm_summary")))
	sb.WriteString(":* ")
	sb.WriteString(format.EscapeMarkdownV2(summary))
	sb.WriteString("\n")

	if description != "" {
		preview := format.TruncateRunes(description, 500)
		sb.WriteString("*")
		sb.WriteString(format.EscapeMarkdownV2(locale.T(lang, "createfast.confirm_description")))
		sb.WriteString(":*\n")
		sb.WriteString(format.EscapeMarkdownV2(preview))
		sb.WriteString("\n")
	}

	if len(files) > 0 {
		sb.WriteString("*")
		sb.WriteString(format.EscapeMarkdownV2(locale.T(lang, "createfast.confirm_attachments")))
		sb.WriteString(":* ")
		sb.WriteString(format.EscapeMarkdownV2(fmt.Sprintf("%d", len(files))))
		sb.WriteString("\n")
	}

	msg := tgbotapi.NewMessage(chatID, sb.String())
	msg.ParseMode = tgbotapi.ModeMarkdownV2
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "createfast.confirm_btn_go"), createFastConfirmCallback+":go"),
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "createfast.confirm_btn_cancel"), createFastConfirmCallback+":cancel"),
		),
	)
	h.sendMessage(msg)
}

// handleCreateFastConfirmCallback resumes the flow after the user confirms
// or cancels the preview.
func (h *Handler) handleCreateFastConfirmCallback(ctx context.Context, cq *tgbotapi.CallbackQuery, parts []string) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	chatID := cq.Message.Chat.ID
	userID := cq.From.ID
	lang := h.getLang(ctx, userID)

	step, data := h.states.Get(userID)
	if step != createFastConfirmPending {
		return
	}
	if len(parts) < 2 {
		return
	}

	if parts[1] == "cancel" {
		h.states.Clear(userID)
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "action.cancelled")))
		return
	}
	if parts[1] != "go" {
		return
	}

	var fields map[string]interface{}
	if err := json.Unmarshal([]byte(data["payload"]), &fields); err != nil {
		h.log.Error().Err(err).Msg("createfast: unmarshal payload for confirmation failed")
		h.states.Clear(userID)
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "create.failed")))
		return
	}
	var files []createFastFile
	if raw := data["files"]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &files); err != nil {
			h.log.Warn().Err(err).Msg("createfast: unmarshal files for confirmation failed (continuing without)")
		}
	}
	epicFieldID := data["epic_field_id"]

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		h.states.Clear(userID)
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected")))
		return
	}

	h.states.Clear(userID)
	h.createFastFinalize(ctx, chatID, userID, user, fields, files, epicFieldID, lang)
}

// createFastFinalize calls CreateIssue and either posts the success message
// + uploads attachments, or — when Jira reports a missing required Epic —
// stashes state and launches the epic picker for a one-tap retry.
func (h *Handler) createFastFinalize(ctx context.Context, chatID, userID int64, user *storage.User, fields map[string]interface{}, files []createFastFile, epicFieldID string, lang locale.Lang) {
	resp, err := h.jiraAPI.CreateIssue(ctx, user, fields)
	if err != nil {
		h.log.Error().Err(err).Msg("createfast: create issue failed")
		detail := extractJiraErrorDetail(err)
		if detail != "" && isEpicRequiredError(detail) {
			if _, already := fields["parent"]; !already {
				if epicFieldID == "" {
					epicFieldID = "parent"
				}
				h.stashCreateFastForEpic(userID, fields, files, epicFieldID)
				h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "create.epic_required_retry")))
				h.showCreateFastEpicPicker(ctx, chatID, userID, user, user.DefaultProject, lang)
				return
			}
		}
		if detail != "" {
			h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "create.failed_detail", detail)))
			return
		}
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "create.failed")))
		return
	}

	issueURL := fmt.Sprintf("%s/browse/%s", user.JiraSiteURL, resp.Key)
	successMsg := tgbotapi.NewMessage(chatID, locale.T(lang, "create.success",
		format.EscapeMarkdownV2(resp.Key), format.EscapeMarkdownV2URL(issueURL)))
	successMsg.ParseMode = tgbotapi.ModeMarkdownV2
	successMsg.DisableWebPagePreview = true
	h.sendMessage(successMsg)

	for _, f := range files {
		h.uploadCreateFastFile(ctx, chatID, user, resp.Key, f, lang)
	}
}

// stashCreateFastForEpic serialises the in-flight CreateIssue payload plus
// any pending Telegram attachments so the epic-callback can resume work
// without re-running fillRequiredDefaults or re-asking the user for input.
func (h *Handler) stashCreateFastForEpic(userID int64, fields map[string]interface{}, files []createFastFile, epicFieldID string) {
	payloadJSON, err := json.Marshal(fields)
	if err != nil {
		h.log.Error().Err(err).Msg("createfast: marshal payload for epic pending failed")
		return
	}
	filesJSON, err := json.Marshal(files)
	if err != nil {
		h.log.Error().Err(err).Msg("createfast: marshal files for epic pending failed")
		return
	}
	h.states.Set(userID, createFastEpicPending, map[string]string{
		"payload":       string(payloadJSON),
		"files":         string(filesJSON),
		"epic_field_id": epicFieldID,
	})
}

// showCreateFastEpicPicker lists active epics in the project as inline
// buttons so the user can attach one without typing.
func (h *Handler) showCreateFastEpicPicker(ctx context.Context, chatID, userID int64, user *storage.User, projectKey string, lang locale.Lang) {
	jql := fmt.Sprintf(`project = %q AND issuetype = Epic AND statusCategory != Done ORDER BY updated DESC`, projectKey)
	result, err := h.jiraAPI.SearchIssues(ctx, user, jql, maxEpicOptions)
	if err != nil {
		h.log.Error().Err(err).Msg("createfast: failed to load epics")
		h.states.Clear(userID)
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "create.failed_epics")))
		return
	}

	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(result.Issues)+1)
	for i := range result.Issues {
		issue := &result.Issues[i]
		label := format.TruncateRunes(fmt.Sprintf("%s — %s", issue.Key, issue.Fields.Summary), 47)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, createFastEpicCallback+":"+issue.Key),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.cancel"), createFastEpicCallback+":cancel"),
	))

	prompt := "create.choose_epic"
	if len(result.Issues) == 0 {
		prompt = "create.no_epics"
	}
	msg := tgbotapi.NewMessage(chatID, locale.T(lang, prompt))
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	h.sendMessage(msg)
}

// handleCreateFastEpicCallback resumes a /createfast flow after the user
// picks an epic (or cancels) from the inline picker.
func (h *Handler) handleCreateFastEpicCallback(ctx context.Context, cq *tgbotapi.CallbackQuery, parts []string) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	chatID := cq.Message.Chat.ID
	userID := cq.From.ID
	lang := h.getLang(ctx, userID)

	step, data := h.states.Get(userID)
	if step != createFastEpicPending {
		return
	}
	if len(parts) < 2 {
		return
	}

	if parts[1] == "cancel" {
		h.states.Clear(userID)
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "action.cancelled")))
		return
	}

	epicKey := parts[1]
	epicFieldID := data["epic_field_id"]
	if epicFieldID == "" {
		epicFieldID = "parent"
	}

	var fields map[string]interface{}
	if err := json.Unmarshal([]byte(data["payload"]), &fields); err != nil {
		h.log.Error().Err(err).Msg("createfast: unmarshal payload for epic retry failed")
		h.states.Clear(userID)
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "create.failed")))
		return
	}
	var files []createFastFile
	if raw := data["files"]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &files); err != nil {
			h.log.Warn().Err(err).Msg("createfast: unmarshal files for epic retry failed (continuing without)")
		}
	}

	if epicFieldID == "parent" {
		fields["parent"] = map[string]string{"key": epicKey}
	} else {
		fields[epicFieldID] = epicKey
	}

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		h.states.Clear(userID)
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected")))
		return
	}

	h.states.Clear(userID)
	h.createFastFinalize(ctx, chatID, userID, user, fields, files, epicFieldID, lang)
}

// splitCreateFastArgs returns (summary, description). The first non-empty
// line is the summary; everything after the first newline is description.
func splitCreateFastArgs(args string) (summary, description string) {
	args = strings.TrimSpace(args)
	if args == "" {
		return "", ""
	}
	parts := strings.SplitN(args, "\n", 2)
	summary = strings.TrimSpace(parts[0])
	if len(parts) == 2 {
		description = strings.TrimSpace(parts[1])
	}
	return summary, description
}

// extractCreateFastFiles returns a slice with at most one file per
// message. Telegram delivers media groups as separate Messages, so we
// don't need to aggregate here. Non-image documents produce an error.
func extractCreateFastFiles(msg *tgbotapi.Message) ([]createFastFile, error) {
	if msg == nil {
		return nil, nil
	}

	if len(msg.Photo) > 0 {
		largest := msg.Photo[len(msg.Photo)-1]
		return []createFastFile{{
			FileID:      largest.FileID,
			FileName:    fmt.Sprintf("photo_%d.jpg", msg.MessageID),
			ContentType: "image/jpeg",
		}}, nil
	}

	if msg.Document != nil {
		if !strings.HasPrefix(msg.Document.MimeType, "image/") {
			return nil, fmt.Errorf("unsupported mime type: %q", msg.Document.MimeType)
		}
		name := msg.Document.FileName
		if name == "" {
			name = fmt.Sprintf("image_%d", msg.MessageID)
		}
		return []createFastFile{{
			FileID:      msg.Document.FileID,
			FileName:    name,
			ContentType: msg.Document.MimeType,
		}}, nil
	}

	return nil, nil
}

// uploadCreateFastFile downloads one Telegram file and POSTs it to the
// freshly created Jira issue. Failures are surfaced in-chat but don't
// unwind the whole /createfast — the issue is already created.
func (h *Handler) uploadCreateFastFile(ctx context.Context, chatID int64, user *storage.User, issueKey string, f createFastFile, lang locale.Lang) {
	fileCfg := tgbotapi.FileConfig{FileID: f.FileID}
	tgFile, err := h.api.GetFile(fileCfg)
	if err != nil {
		h.log.Error().Err(err).Str("file_id", f.FileID).Msg("createfast: tg get_file failed")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "createfast.attach_failed", err.Error())))
		return
	}

	url := tgFile.Link(h.api.Token)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "createfast.attach_failed", err.Error())))
		return
	}
	resp, err := h.httpClient.Do(req)
	if err != nil {
		h.log.Error().Err(err).Msg("createfast: tg download failed")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "createfast.attach_failed", err.Error())))
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "createfast.attach_failed", resp.Status)))
		return
	}

	if _, err := h.jiraAPI.UploadAttachment(ctx, user, issueKey, f.FileName, f.ContentType, resp.Body); err != nil {
		h.log.Error().Err(err).Str("issue", issueKey).Msg("createfast: jira upload failed")
		detail := err.Error()
		if d := extractJiraErrorDetail(err); d != "" {
			detail = d
		}
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "createfast.attach_failed", detail)))
		return
	}

	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "createfast.attached",
		format.EscapeMarkdownV2(issueKey)))
	msg.ParseMode = tgbotapi.ModeMarkdownV2
	h.sendMessage(msg)
}

// fillRequiredDefaults queries create-meta for the chosen project/issue type
// and auto-populates any required custom field that has an allowed-value
// list by selecting the first allowed value. This keeps /createfast a
// single-shot command on projects with mandatory selectors (e.g. "Продукт")
// without forcing users into an interactive wizard. Returns the id of a
// required epic-link field when one is detected — the caller surfaces that
// to the epic picker wizard instead of auto-filling (picking "first allowed
// epic" silently would attach the task to the wrong parent).
func (h *Handler) fillRequiredDefaults(ctx context.Context, user *storage.User, fields map[string]interface{}) (epicFieldID string, _ error) {
	metaFields, err := h.jiraAPI.GetCreateMetaFields(ctx, user, user.DefaultProject, user.DefaultIssueTypeID)
	if err != nil {
		h.log.Warn().Err(err).Str("project", user.DefaultProject).Msg("createfast: could not fetch createmeta fields")
		return "", nil
	}

	for i := range metaFields {
		f := &metaFields[i]
		if !f.Required {
			continue
		}
		if _, already := fields[f.FieldID]; already {
			continue
		}
		if isEpicLinkField(f) {
			if epicFieldID == "" {
				epicFieldID = f.FieldID
			}
			continue
		}
		if len(f.AllowedValues) == 0 {
			continue
		}

		first := f.AllowedValues[0]
		if first.ID == "" {
			continue
		}

		switch f.Schema.Type {
		case "array":
			fields[f.FieldID] = []map[string]string{{"id": first.ID}}
		case "option", "priority", "issuetype", "project", "user", "group", "resolution":
			fields[f.FieldID] = map[string]string{"id": first.ID}
		default:
			// For untyped or string-like fields with allowed values, Jira
			// commonly accepts the id-wrapped form too.
			fields[f.FieldID] = map[string]string{"id": first.ID}
		}

		h.log.Info().
			Str("field_id", f.FieldID).
			Str("field_name", f.Name).
			Str("value_id", first.ID).
			Str("value_name", first.Name).
			Msg("createfast: auto-filled required field with first allowed value")
	}

	return epicFieldID, nil
}

// isEpicLinkField detects required epic-link fields so they bypass silent
// auto-fill and instead route through the epic picker wizard.
func isEpicLinkField(f *jira.CreateMetaField) bool {
	if f == nil {
		return false
	}
	if f.Schema.Custom == "com.pyxis.greenhopper.jira:gh-epic-link" {
		return true
	}
	if f.Key == "customfield_10014" || f.FieldID == "customfield_10014" {
		return true
	}
	if f.FieldID == "parent" && strings.EqualFold(f.Schema.Type, "issuelink") {
		return true
	}
	if strings.Contains(strings.ToLower(f.Name), "epic") {
		return true
	}
	return false
}

// preferredDefaultIssueTypes lists the issue-type names we try to pick, in
// order, when the user has a default project but no explicit default issue
// type yet. "Dev task" matches the convention in this workspace; "Task" is
// kept as a safety fallback for other Jira instances.
var preferredDefaultIssueTypes = []string{"Dev task", "Task"}

// resolveDefaultIssueType picks a sensible issue type for the user's default
// project when they haven't explicitly chosen one yet. Walks
// preferredDefaultIssueTypes in order, then falls back to the first
// non-subtask entry.
func (h *Handler) resolveDefaultIssueType(ctx context.Context, user *storage.User) (id, name string, err error) {
	types, err := h.jiraAPI.GetCreateMetaIssueTypes(ctx, user, user.DefaultProject)
	if err != nil {
		return "", "", err
	}

	byName := make(map[string]*jira.CreateMetaIssueType, len(types))
	var fallback *jira.CreateMetaIssueType
	for i := range types {
		t := &types[i]
		if t.Subtask {
			continue
		}
		byName[strings.ToLower(t.Name)] = t
		if fallback == nil {
			fallback = t
		}
	}

	for _, pref := range preferredDefaultIssueTypes {
		if t, ok := byName[strings.ToLower(pref)]; ok {
			return t.ID, t.Name, nil
		}
	}
	if fallback != nil {
		return fallback.ID, fallback.Name, nil
	}
	return "", "", nil
}
