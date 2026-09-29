package tui

import "supercli/internal/system/uilang"

func normalizeLanguage(language string) string {
	if normalized := uilang.Normalize(language); normalized != "" {
		return normalized
	}
	// Empty Options.Language is common in unit tests and embedded users. Keep
	// their historical English default; executable entrypoints always pass the
	// detected/persisted language explicitly.
	return uilang.English
}

func textFor(language, key string) string {
	return uilang.Text(language, key)
}

func (m Model) tr(key string) string {
	return textFor(m.language, key)
}
