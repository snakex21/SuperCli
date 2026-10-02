package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// LoadMediaGeneration reads only the operator-owned portable config. Project
// configuration must never redirect environment credentials to another host.
func LoadMediaGeneration(dataDir string) (MediaGenerationConf, error) {
	if strings.TrimSpace(dataDir) == "" {
		return MediaGenerationConf{}, fmt.Errorf("portable data directory is required for media generation")
	}
	cfg, err := LoadToml(filepath.Join(dataDir, "config.toml"))
	return cfg.MediaGeneration, err
}
