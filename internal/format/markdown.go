package format

import "strings"

var markdownReplacer = strings.NewReplacer(
	"_", "\\_",
	"*", "\\*",
	"`", "\\`",
	"[", "\\[",
	"]", "\\]",
)

// EscapeMarkdown escapes special Telegram Markdown characters.
func EscapeMarkdown(s string) string {
	return markdownReplacer.Replace(s)
}

var markdownStripper = strings.NewReplacer(
	"\\_", "_",
	"\\*", "*",
	"\\`", "`",
	"\\[", "[",
	"\\]", "]",
)

// StripMarkdownEscapes removes escape characters added by EscapeMarkdown.
func StripMarkdownEscapes(s string) string {
	return markdownStripper.Replace(s)
}
