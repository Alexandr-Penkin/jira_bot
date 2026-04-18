package format

import (
	"testing"

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
