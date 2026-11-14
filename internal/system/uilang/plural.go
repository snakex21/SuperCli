package uilang

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Plural messages keep every CLDR form in one catalog value, for example
// "one {# tool} few {# tools} many {# tools} other {# tools}"; "#" is the
// number. The GUI reads the same format with Intl.PluralRules, so both
// interfaces pick the same form. A missing form falls back to "other".
var pluralForm = regexp.MustCompile(`(zero|one|two|few|many|other)\s*\{([^{}]*)\}`)

// Parsed forms per language and key; catalogs are immutable, and the TUI
// formats plurals on its render path.
var pluralForms sync.Map // pluralKey -> map[string]string

type pluralKey struct{ language, key string }

func pluralFormsFor(language, key string) map[string]string {
	cacheKey := pluralKey{language, key}
	if forms, ok := pluralForms.Load(cacheKey); ok {
		return forms.(map[string]string)
	}
	forms := map[string]string{}
	for _, m := range pluralForm.FindAllStringSubmatch(Text(language, key), -1) {
		forms[m[1]] = m[2]
	}
	pluralForms.Store(cacheKey, forms)
	return forms
}

// Plural returns the catalog message key for n in language.
func Plural(language, key string, n int) string {
	forms := pluralFormsFor(language, key)
	form, ok := forms[PluralCategory(language, n)]
	if !ok {
		form, ok = forms["other"]
	}
	if !ok {
		return strconv.Itoa(n) + " " + Text(language, key)
	}
	return strings.ReplaceAll(form, "#", strconv.Itoa(n))
}

// PluralCategory is the CLDR cardinal category of an integer in the UI
// languages SuperCli ships (zero/one/two/few/many/other).
func PluralCategory(language string, n int) string {
	if n < 0 {
		n = -n
	}
	mod10, mod100 := n%10, n%100
	switch Normalize(language) {
	case "fr", "pt-BR":
		if n <= 1 {
			return "one"
		}
	case "pl":
		switch {
		case n == 1:
			return "one"
		case mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14):
			return "few"
		}
		return "many"
	case "ru", "uk":
		switch {
		case mod10 == 1 && mod100 != 11:
			return "one"
		case mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14):
			return "few"
		}
		return "many"
	case "hr", "sr-Latn":
		switch {
		case mod10 == 1 && mod100 != 11:
			return "one"
		case mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14):
			return "few"
		}
	case "cs", "sk":
		switch {
		case n == 1:
			return "one"
		case n >= 2 && n <= 4:
			return "few"
		}
	case "sl":
		switch {
		case mod100 == 1:
			return "one"
		case mod100 == 2:
			return "two"
		case mod100 == 3 || mod100 == 4:
			return "few"
		}
	case "lt":
		switch {
		case mod10 == 1 && (mod100 < 11 || mod100 > 19):
			return "one"
		case mod10 >= 2 && (mod100 < 11 || mod100 > 19):
			return "few"
		}
	case "lv":
		switch {
		case mod10 == 0 || (mod100 >= 11 && mod100 <= 19):
			return "zero"
		case mod10 == 1 && mod100 != 11:
			return "one"
		}
	case "ro":
		switch {
		case n == 1:
			return "one"
		case n == 0 || (mod100 >= 2 && mod100 <= 19):
			return "few"
		}
	default:
		if n == 1 {
			return "one"
		}
	}
	return "other"
}
