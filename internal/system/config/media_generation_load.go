package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// MediaGenerationProviderKey returns the base URL and key of a provider from
// the portable global config only (never a project file).
func MediaGenerationProviderKey(dataDir, name string) (string, string, bool) {
	if strings.TrimSpace(dataDir) == "" || strings.TrimSpace(name) == "" {
		return "", "", false
	}
	cfg, err := LoadToml(filepath.Join(dataDir, "config.toml"))
	if err != nil {
		return "", "", false
	}
	for _, p := range cfg.Providers {
		if p.Name == name {
			return p.BaseURL, p.APIKey, true
		}
	}
	return "", "", false
}

// LoadMediaGeneration reads only the operator-owned portable config. Project
// configuration must never redirect environment credentials to another host.
func LoadMediaGeneration(dataDir string) (MediaGenerationConf, error) {
	if strings.TrimSpace(dataDir) == "" {
		return MediaGenerationConf{}, fmt.Errorf("portable data directory is required for media generation")
	}
	cfg, err := LoadToml(filepath.Join(dataDir, "config.toml"))
	return cfg.MediaGeneration, err
}
