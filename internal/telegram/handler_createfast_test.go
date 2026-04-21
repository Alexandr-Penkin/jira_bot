package telegram

import (
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
)

func TestSplitCreateFastArgs(t *testing.T) {
	tests := []struct {
		name            string
		in              string
		wantSummary     string
		wantDescription string
	}{
		{"empty", "", "", ""},
		{"whitespace only", "   \n\t  ", "", ""},
		{"single line", "Fix login bug", "Fix login bug", ""},
		{"two lines", "Fix login\nSafari users cannot log in.", "Fix login", "Safari users cannot log in."},
		{"multiline description", "Summary line\nLine 1\nLine 2\nLine 3", "Summary line", "Line 1\nLine 2\nLine 3"},
		{"leading newline", "\nFirst real line\nRest", "First real line", "Rest"},
		{"trim summary spaces", "   Trimmed   \nDescription", "Trimmed", "Description"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			summary, description := splitCreateFastArgs(tc.in)
			assert.Equal(t, tc.wantSummary, summary)
			assert.Equal(t, tc.wantDescription, description)
		})
	}
}

func TestCaptionCommand(t *testing.T) {
	t.Run("no caption", func(t *testing.T) {
		msg := &tgbotapi.Message{}
		cmd, args := captionCommand(msg)
		assert.Empty(t, cmd)
		assert.Empty(t, args)
	})

	t.Run("caption without bot_command entity", func(t *testing.T) {
		msg := &tgbotapi.Message{Caption: "just a caption"}
		cmd, args := captionCommand(msg)
		assert.Empty(t, cmd)
		assert.Empty(t, args)
	})

	t.Run("createfast caption", func(t *testing.T) {
		msg := &tgbotapi.Message{
			Caption: "/createfast Fix bug\nDetails here",
			CaptionEntities: []tgbotapi.MessageEntity{
				{Type: "bot_command", Offset: 0, Length: len("/createfast")},
			},
		}
		cmd, args := captionCommand(msg)
		assert.Equal(t, "createfast", cmd)
		assert.Equal(t, "Fix bug\nDetails here", args)
	})

	t.Run("createfast with bot username", func(t *testing.T) {
		msg := &tgbotapi.Message{
			Caption: "/createfast@SleepJiraBot Fix bug",
			CaptionEntities: []tgbotapi.MessageEntity{
				{Type: "bot_command", Offset: 0, Length: len("/createfast@SleepJiraBot")},
			},
		}
		cmd, args := captionCommand(msg)
		assert.Equal(t, "createfast", cmd)
		assert.Equal(t, "Fix bug", args)
	})

	t.Run("non-command caption entity", func(t *testing.T) {
		msg := &tgbotapi.Message{
			Caption: "see @mention here",
			CaptionEntities: []tgbotapi.MessageEntity{
				{Type: "mention", Offset: 4, Length: 8},
			},
		}
		cmd, args := captionCommand(msg)
		assert.Empty(t, cmd)
		assert.Empty(t, args)
	})

	t.Run("bot_command not at offset 0 is ignored", func(t *testing.T) {
		msg := &tgbotapi.Message{
			Caption: "hello /createfast",
			CaptionEntities: []tgbotapi.MessageEntity{
				{Type: "bot_command", Offset: 6, Length: len("/createfast")},
			},
		}
		cmd, args := captionCommand(msg)
		assert.Empty(t, cmd)
		assert.Empty(t, args)
	})
}

func TestExtractCreateFastFiles_Photo(t *testing.T) {
	msg := &tgbotapi.Message{
		MessageID: 17,
		Photo: []tgbotapi.PhotoSize{
			{FileID: "small", Width: 90, Height: 90},
			{FileID: "medium", Width: 320, Height: 320},
			{FileID: "large", Width: 1280, Height: 1280},
		},
	}
	files, err := extractCreateFastFiles(msg)
	assert.NoError(t, err)
	if assert.Len(t, files, 1) {
		assert.Equal(t, "large", files[0].FileID)
		assert.Equal(t, "photo_17.jpg", files[0].FileName)
		assert.Equal(t, "image/jpeg", files[0].ContentType)
	}
}

func TestExtractCreateFastFiles_ImageDocument(t *testing.T) {
	msg := &tgbotapi.Message{
		MessageID: 42,
		Document: &tgbotapi.Document{
			FileID:   "doc-id",
			FileName: "screenshot.png",
			MimeType: "image/png",
		},
	}
	files, err := extractCreateFastFiles(msg)
	assert.NoError(t, err)
	if assert.Len(t, files, 1) {
		assert.Equal(t, "doc-id", files[0].FileID)
		assert.Equal(t, "screenshot.png", files[0].FileName)
		assert.Equal(t, "image/png", files[0].ContentType)
	}
}

func TestExtractCreateFastFiles_NonImageDocumentRejected(t *testing.T) {
	msg := &tgbotapi.Message{
		Document: &tgbotapi.Document{
			FileID:   "doc-id",
			FileName: "report.pdf",
			MimeType: "application/pdf",
		},
	}
	files, err := extractCreateFastFiles(msg)
	assert.Error(t, err)
	assert.Nil(t, files)
}

func TestExtractCreateFastFiles_NilMessage(t *testing.T) {
	files, err := extractCreateFastFiles(nil)
	assert.NoError(t, err)
	assert.Nil(t, files)
}

func TestExtractCreateFastFiles_DocumentWithoutName(t *testing.T) {
	msg := &tgbotapi.Message{
		MessageID: 7,
		Document: &tgbotapi.Document{
			FileID:   "doc-id",
			MimeType: "image/jpeg",
		},
	}
	files, err := extractCreateFastFiles(msg)
	assert.NoError(t, err)
	if assert.Len(t, files, 1) {
		assert.Equal(t, "image_7", files[0].FileName)
	}
}
