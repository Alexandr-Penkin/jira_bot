package format

import (
	"strings"
	"unicode/utf8"
)

var markdownReplacer = strings.NewReplacer(
	"_", "\\_",
	"*", "\\*",
	"`", "\\`",
	"[", "\\[",
	"]", "\\]",
)

// EscapeMarkdown escapes special Telegram legacy Markdown characters.
func EscapeMarkdown(s string) string {
	return markdownReplacer.Replace(s)
}

// markdownV2Special lists all characters reserved by Telegram MarkdownV2
// outside of code/pre blocks. See https://core.telegram.org/bots/api#markdownv2-style.
const markdownV2Special = "_*[]()~`>#+-=|{}.!\\"

// EscapeMarkdownV2 escapes every character reserved by Telegram MarkdownV2.
// Must be used for message bodies sent with ParseMode = MarkdownV2, otherwise
// any unescaped reserved char (e.g. `(` in a summary) causes a 400 from the API.
func EscapeMarkdownV2(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 128 && strings.ContainsRune(markdownV2Special, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

var markdownV2URLReplacer = strings.NewReplacer(
	"\\", "\\\\",
	")", "\\)",
)

// EscapeMarkdownV2URL escapes characters that must be escaped inside the
// "(...)" URL part of a MarkdownV2 inline link: `)` and `\`. All other
// characters (including `.`, `-`, `_`) are allowed there as-is.
func EscapeMarkdownV2URL(s string) string {
	return markdownV2URLReplacer.Replace(s)
}

var markdownStripper = strings.NewReplacer(
	"\\_", "_",
	"\\*", "*",
	"\\`", "`",
	"\\[", "[",
	"\\]", "]",
	"\\(", "(",
	"\\)", ")",
	"\\~", "~",
	"\\>", ">",
	"\\#", "#",
	"\\+", "+",
	"\\-", "-",
	"\\=", "=",
	"\\|", "|",
	"\\{", "{",
	"\\}", "}",
	"\\.", ".",
	"\\!", "!",
	"\\\\", "\\",
)

// StripMarkdownEscapes removes backslash-escapes for both legacy Markdown and
// MarkdownV2 reserved characters. Used when a formatted send fails and we
// retry as plain text.
func StripMarkdownEscapes(s string) string {
	return markdownStripper.Replace(s)
}

// TruncateRunes shortens s to at most n runes, appending "..." when truncated.
// Unlike slicing by bytes, it never cuts in the middle of a multi-byte rune,
// so the result is always valid UTF-8 (Telegram rejects messages that aren't).
func TruncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	i, count := 0, 0
	for i < len(s) && count < n {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		count++
	}
	return s[:i] + "..."
}
