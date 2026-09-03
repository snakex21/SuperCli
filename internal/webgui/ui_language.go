package webgui

import (
	"supercli/internal/system/config"
	"supercli/internal/system/uilang"
)

// uiLanguage returns the resolved UI language ("en" or "pl") from config.toml,
// the same source the browser reads through /api/settings. Server-side model
// prompts that produce user-facing prose read it so the language switch really
// changes what the model writes, instead of always answering in one language.
func (s *Server) uiLanguage() string {
	resolved, _ := config.ResolveConfig(s.eng.DataDir(), s.eng.Home(), "")
	language, _ := config.EnsureLanguage(s.eng.DataDir(), s.eng.Home(), resolved.Language)
	return language
}

// respondInLanguage is the instruction appended to an English prompt so the
// model answers in the user's language. The prompts themselves stay English —
// models follow English instructions more reliably — and only the requested
// output language varies.
func respondInLanguage(language string) string {
	if uilang.IsPolish(language) {
		return "Respond in Polish."
	}
	return "Respond in English."
}
