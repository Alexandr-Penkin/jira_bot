package logger

import (
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
)

func TestNew_KnownLevel(t *testing.T) {
	l := New("debug")
	assert.Equal(t, zerolog.DebugLevel, l.GetLevel())
}

func TestNew_InvalidLevelFallsBackToInfo(t *testing.T) {
	// A typo in LOG_LEVEL must not panic — the logger should stay usable
	// at the Info default so services start cleanly in prod.
	l := New("nope")
	assert.Equal(t, zerolog.InfoLevel, l.GetLevel())
}

func TestNew_EmptyLevelParsesToNoLevel(t *testing.T) {
	// zerolog.ParseLevel("") returns NoLevel without an error, so the
	// fallback branch doesn't fire — we keep NoLevel as-is.
	l := New("")
	assert.Equal(t, zerolog.NoLevel, l.GetLevel())
}

func TestNew_AllStandardLevels(t *testing.T) {
	cases := []struct {
		in   string
		want zerolog.Level
	}{
		{"trace", zerolog.TraceLevel},
		{"debug", zerolog.DebugLevel},
		{"info", zerolog.InfoLevel},
		{"warn", zerolog.WarnLevel},
		{"error", zerolog.ErrorLevel},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, New(c.in).GetLevel(), "level=%s", c.in)
	}
}
