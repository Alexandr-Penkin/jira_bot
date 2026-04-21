package telegram

import (
	"context"
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

const createFastCommand = "createfast"

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

	resp, err := h.jiraAPI.CreateIssue(ctx, user, fields)
	if err != nil {
		h.log.Error().Err(err).Msg("createfast: create issue failed")
		if detail := extractJiraErrorDetail(err); detail != "" {
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
