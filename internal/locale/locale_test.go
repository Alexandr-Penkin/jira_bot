package locale

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValid(t *testing.T) {
	assert.True(t, Valid(EN))
	assert.True(t, Valid(RU))
	assert.False(t, Valid(Lang("fr")))
	assert.False(t, Valid(Lang("")))
}

func TestFromString_KnownLang(t *testing.T) {
	assert.Equal(t, EN, FromString("en"))
	assert.Equal(t, RU, FromString("ru"))
}

func TestFromString_UnknownFallsBackToDefault(t *testing.T) {
	assert.Equal(t, Default, FromString("fr"))
	assert.Equal(t, Default, FromString(""))
}

func TestT_KnownKeyEnglish(t *testing.T) {
	got := T(EN, "action.cancelled")
	assert.Equal(t, "Action cancelled.", got)
}

func TestT_KnownKeyRussian(t *testing.T) {
	// The Russian dict must have its own translation (different string).
	got := T(RU, "action.cancelled")
	assert.NotEmpty(t, got)
	assert.NotEqual(t, "action.cancelled", got, "missing ru translation returns the key verbatim")
}

func TestT_UnknownKeyReturnsKeyVerbatim(t *testing.T) {
	// Missing keys in both dicts fall through to returning the key so a
	// ui-side regression becomes visible instead of silently blank.
	got := T(EN, "nonexistent.key.zzz")
	assert.Equal(t, "nonexistent.key.zzz", got)
}

func TestT_UnknownLangFallsBackToDefault(t *testing.T) {
	got := T(Lang("fr"), "action.cancelled")
	assert.Equal(t, "Action cancelled.", got)
}

func TestT_WithFormatArgs(t *testing.T) {
	// Use an arbitrary key that contains %s. If this test starts failing
	// because no such key exists, rewrite with a format-capable key.
	translations[EN]["__test.fmt"] = "hello %s"
	defer delete(translations[EN], "__test.fmt")

	got := T(EN, "__test.fmt", "world")
	assert.Equal(t, "hello world", got)
}

func TestLangName(t *testing.T) {
	assert.Equal(t, "English", LangName(EN))
	assert.Equal(t, "Русский", LangName(RU))
	assert.Equal(t, "fr", LangName(Lang("fr")), "unknown lang returns its raw code")
}
