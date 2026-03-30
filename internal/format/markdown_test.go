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
