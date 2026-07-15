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

// Normalize accepts common locale forms and returns a supported language.
func Normalize(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "-")
	switch {
	case value == Polish, strings.HasPrefix(value, "pl-"):
		return Polish
	case value == English, strings.HasPrefix(value, "en-"):
		return English
	default:
		return ""
	}
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
		if language := Normalize(os.Getenv(key)); language != "" {
			return language
		}
	}
	return English
}

func IsPolish(language string) bool { return Normalize(language) == Polish }
