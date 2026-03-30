package locale

import "fmt"

// Lang represents a supported language.
type Lang string

const (
	EN Lang = "en"
	RU Lang = "ru"
)

// Default is the default language.
const Default = EN

// Valid returns true if the language is supported.
func Valid(l Lang) bool {
	return l == EN || l == RU
}

// FromString converts a string to Lang, falling back to Default.
func FromString(s string) Lang {
	l := Lang(s)
	if Valid(l) {
		return l
	}
	return Default
}

// T returns a localized string for the given key.
// Optional args are passed to fmt.Sprintf if the template contains %s/%d/etc.
func T(lang Lang, key string, args ...any) string {
	dict, ok := translations[lang]
	if !ok {
		dict = translations[Default]
	}
	tmpl, ok := dict[key]
	if !ok {
		tmpl = translations[Default][key]
	}
	if tmpl == "" {
		return key
	}
	if len(args) > 0 {
		return fmt.Sprintf(tmpl, args...)
	}
	return tmpl
}

// LangName returns a display name for the language.
func LangName(l Lang) string {
	switch l {
	case RU:
		return "Русский"
	case EN:
		return "English"
	default:
		return string(l)
	}
}

var translations = map[Lang]map[string]string{
	EN: en,
	RU: ru,
}
