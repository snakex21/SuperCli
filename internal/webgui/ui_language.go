package webgui

import (
	"fmt"
	"path/filepath"
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

// Native dialogs cannot use the browser dictionary. Read the current portable
// preference on each close request, including changes made while the app runs.
func nativeCloseText(dataDir, appName string) string {
	cfg, _ := config.LoadToml(filepath.Join(dataDir, "config.toml"))
	if uilang.IsPolish(uilang.Resolve(cfg.Language)) {
		return fmt.Sprintf("Zamknąć %s?\n\nTrwające zadanie zostanie zatrzymane.", appName)
	}
	return fmt.Sprintf("Close %s?\n\nThe active task will be stopped.", appName)
}
