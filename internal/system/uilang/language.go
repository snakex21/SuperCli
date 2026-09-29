// Package uilang owns the small, shared UI-language contract used by both
// the terminal and desktop front-ends. The persisted value lives in the
// portable config.toml; detection is only a first-run fallback.
package uilang

import (
	"os"
	"strings"
)

const (
	English = "en"
	Polish  = "pl"
)

// Language uses native names so a user can recognize their language even
// before the interface has been translated.
type Language struct {
	Code string `json:"code"`
	Name string `json:"name"`
	Dir  string `json:"dir"`
}

var languages = []Language{
	{"en", "English", "ltr"}, {"bg", "Български", "ltr"},
	{"cs", "Čeština", "ltr"}, {"da", "Dansk", "ltr"},
	{"de", "Deutsch", "ltr"}, {"el", "Ελληνικά", "ltr"},
	{"es", "Español", "ltr"}, {"et", "Eesti", "ltr"},
	{"fi", "Suomi", "ltr"}, {"fr", "Français", "ltr"},
	{"hr", "Hrvatski", "ltr"}, {"hu", "Magyar", "ltr"},
	{"it", "Italiano", "ltr"}, {"lt", "Lietuvių", "ltr"},
	{"lv", "Latviešu", "ltr"}, {"nb", "Norsk bokmål", "ltr"},
	{"nl", "Nederlands", "ltr"}, {"pl", "Polski", "ltr"},
	{"pt-BR", "Português (Brasil)", "ltr"}, {"ro", "Română", "ltr"},
	{"ru", "Русский", "ltr"}, {"sk", "Slovenčina", "ltr"},
	{"sl", "Slovenščina", "ltr"}, {"sr-Latn", "Srpski (latinica)", "ltr"},
	{"sv", "Svenska", "ltr"}, {"tr", "Türkçe", "ltr"},
	{"uk", "Українська", "ltr"},
}

// Languages returns an independent copy of the shared picker metadata.
func Languages() []Language { return append([]Language(nil), languages...) }

func Name(code string) string {
	code = Normalize(code)
	for _, language := range languages {
		if language.Code == code {
			return language.Name
		}
	}
	return "English"
}

// Normalize accepts common locale forms and returns a supported language.
func Normalize(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "-")
	value = strings.SplitN(value, ":", 2)[0]
	value = strings.SplitN(value, ".", 2)[0]
	value = strings.SplitN(value, "@", 2)[0]
	base := strings.SplitN(value, "-", 2)[0]
	switch base {
	case "pt":
		return "pt-BR"
	case "sr":
		return "sr-Latn"
	case "no":
		return "nb"
	}
	for _, language := range languages {
		if base == strings.ToLower(language.Code) {
			return language.Code
		}
	}
	return ""
}

// Resolve returns a persisted supported language, or detects it once from the
// host. Unsupported host locales deliberately fall back to English.
func Resolve(saved string) string {
	if language := Normalize(saved); language != "" {
		return language
	}
	if language := detectSystem(); language != "" {
		return language
	}
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG", "LANGUAGE"} {
		for _, candidate := range strings.Split(os.Getenv(key), ":") {
			if language := Normalize(candidate); language != "" {
				return language
			}
		}
	}
	return English
}

func IsPolish(language string) bool { return Normalize(language) == Polish }
