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

func textFor(language, english, polish string) string {
	if uilang.IsPolish(language) {
		return polish
	}
	return english
}

func (m Model) tr(english, polish string) string {
	return textFor(m.language, english, polish)
}
