package format

import (
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

func TestEscapeMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty string", "", ""},
		{"no special chars", "hello world", "hello world"},
		{"underscore", "snake_case", "snake\\_case"},
		{"asterisk", "*bold*", "\\*bold\\*"},
		{"backtick", "`code`", "\\`code\\`"},
		{"bracket", "[link]", "\\[link\\]"},
		{"all special chars", "_*`[]", "\\_\\*\\`\\[\\]"},
		{"mixed content", "hello_world *test* `code` [link]", "hello\\_world \\*test\\* \\`code\\` \\[link\\]"},
		{"multiple underscores", "a_b_c", "a\\_b\\_c"},
		{"closing bracket", "]", "\\]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, EscapeMarkdown(tt.input))
		})
	}
}

func TestStripMarkdownEscapes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty", "", ""},
		{"no escapes", "hello world", "hello world"},
		{"all special chars", "\\_\\*\\`\\[\\]", "_*`[]"},
		{"mixed content", "hello\\_world \\*test\\*", "hello_world *test*"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, StripMarkdownEscapes(tt.input))
		})
	}
}

func TestEscapeMarkdownV2(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"plain", "hello", "hello"},
		{"parens", "foo (bar)", "foo \\(bar\\)"},
		{"dot and dash", "ABC-123.json", "ABC\\-123\\.json"},
		{"all reserved", "_*[]()~`>#+-=|{}.!\\", "\\_\\*\\[\\]\\(\\)\\~\\`\\>\\#\\+\\-\\=\\|\\{\\}\\.\\!\\\\"},
		{"cyrillic untouched", "Привет, мир!", "Привет, мир\\!"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, EscapeMarkdownV2(tt.input))
		})
	}
}

func TestEscapeMarkdownV2URL(t *testing.T) {
	assert.Equal(t, "https://example.atlassian.net/browse/ABC-1", EscapeMarkdownV2URL("https://example.atlassian.net/browse/ABC-1"))
	assert.Equal(t, "a\\)b\\\\c", EscapeMarkdownV2URL("a)b\\c"))
}

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		n        int
		expected string
	}{
		{"shorter than limit", "hi", 10, "hi"},
		{"exact limit", "abcde", 5, "abcde"},
		{"ascii truncation", "abcdefgh", 5, "abcde..."},
		{"multibyte never cuts mid-rune", "абвгдежзий", 4, "абвг..."},
		{"emoji safe", "🙂🙃🙂🙃🙂", 3, "🙂🙃🙂..."},
		{"zero", "anything", 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateRunes(tt.input, tt.n)
			assert.Equal(t, tt.expected, got)
			assert.True(t, utf8.ValidString(got), "result must be valid UTF-8")
		})
	}
}

func TestEscapeStripRoundtrip(t *testing.T) {
	// EscapeMarkdown then StripMarkdownEscapes must reproduce the input
	// for any string that does not itself contain an existing backslash-
	// escape. This is the only supported roundtrip.
	inputs := []string{
		"plain",
		"snake_case",
		"*bold*",
		"`code`",
		"[link](url)",
		"mix_*with*_many`symbols`[here]",
	}
	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			assert.Equal(t, in, StripMarkdownEscapes(EscapeMarkdown(in)))
		})
	}
}
