package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// HeadlessConf is operator-owned scope, never model or project configuration.
// Empty targets or actions grant no access. Mutations still require consent.
type HeadlessConf struct {
	Targets map[string]HeadlessTargetConf `toml:"targets"`
}
type HeadlessTargetConf struct {
	Protocol       string   `toml:"protocol"`
	Endpoint       string   `toml:"endpoint"`
	AllowedActions []string `toml:"allowed_actions"`
}

func LoadHeadless(dataDir string) (HeadlessConf, error) {
	if strings.TrimSpace(dataDir) == "" {
		return HeadlessConf{}, fmt.Errorf("portable data directory is required for headless targets")
	}
	cfg, err := LoadToml(filepath.Join(dataDir, "config.toml"))
	return cfg.Headless, err
}
