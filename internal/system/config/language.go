package config

import "supercli/internal/system/uilang"

// EnsureLanguage resolves the shared UI language and persists first-run
// detection in the global portable config. A project config may override the
// effective language, but automatic detection never copies project settings
// into the global file.
func EnsureLanguage(dataDir, cwd, preferred string) (string, error) {
	language := uilang.Resolve(preferred)
	if uilang.Normalize(preferred) != "" {
		return language, nil
	}
	return language, SetLanguage(dataDir, cwd, language)
}

// SetLanguage updates only the global language field, preserving providers,
// credentials and every unrelated knob.
func SetLanguage(dataDir, cwd, language string) error {
	language = uilang.Normalize(language)
	if language == "" {
		language = uilang.English
	}
	path, _ := FindTomlPaths(dataDir, cwd)
	cfg, err := LoadToml(path)
	if err != nil {
		return err
	}
	if cfg.Language == language {
		return nil
	}
	cfg.Language = language
	return SaveToml(path, cfg)
}
