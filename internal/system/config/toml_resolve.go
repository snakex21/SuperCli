// Package config loads SuperCli's runtime configuration from
// TOML files, environment variables, and command-line flags.
//
// Hierarchy (highest priority wins):
//
//	CLI flags > env vars > project config > global config
//
// Global config:  <data dir>/config.toml (portable: supercli-data next to the binary)
// Project config: <cwd>/.supercli/config.toml
// CLI override:   --config <path>
package config

import (
	"os"
	"strings"
)

func ResolveConfig(dataDir, cwd, configPath string) (TomlConfig, error) {
	// Layer 1: global config.
	globalPath, projectPath := FindTomlPaths(dataDir, cwd)
	global, err := LoadToml(globalPath)
	if err != nil {
		return TomlConfig{}, err
	}

	// Layer 2: project config (overrides global).
	project, err := LoadToml(projectPath)
	if err != nil {
		return TomlConfig{}, err
	}
	mergeToml(&global, project)

	// Layer 3: --config <path> override.
	if configPath != "" {
		custom, err := LoadToml(configPath)
		if err != nil {
			return TomlConfig{}, err
		}
		mergeToml(&global, custom)
	}

	return global, nil
}

// ResolveProviderConf finds a configured provider by name in the resolved
// project view, then falls back to the global provider store. Project provider
// lists intentionally replace the global list, but UI state may still name a
// global provider selected from the shared provider manager. This fallback
// keeps the remembered model/provider pair attached to its base URL and key on
// restart without weakening project precedence for duplicate names.
func ResolveProviderConf(dataDir string, resolved TomlConfig, name string) (ProviderConf, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ProviderConf{}, false
	}
	for _, p := range resolved.Providers {
		if p.Name == name {
			return p, true
		}
	}
	globalPath, _ := FindTomlPaths(dataDir, "")
	global, err := LoadToml(globalPath)
	if err != nil {
		return ProviderConf{}, false
	}
	for _, p := range global.Providers {
		if p.Name == name {
			return p, true
		}
	}
	return ProviderConf{}, false
}

// ApplyTomlToConfig applies resolved TOML values onto a
// Config that already has env+flag values applied. The TOML
// values act as defaults — env and flags always win. Fields
// that were already set by env/flags (non-zero) are kept.
func ApplyTomlToConfig(c *Config, t TomlConfig) {
	// Only apply TOML values where Config fields are still
	// at their zero values (meaning no env/flag set them).
	if c.Model == "" && t.DefaultModel != "" {
		c.Model = t.DefaultModel
	}

	// Provider resolution: DefaultProvider (by name) is the active
	// provider selected by the UI. Apply the matching provider's
	// type/base/key together so startup behaves exactly like a later
	// /model swap. Without this, stale top-level/env defaults can make
	// the first request use the right model with the wrong credentials.
	if t.DefaultProvider != "" {
		for _, p := range t.Providers {
			if p.Name == t.DefaultProvider {
				c.Provider = p.Type
				c.BaseURL = p.BaseURL
				c.APIKey = p.APIKey
				break
			}
		}
	} else if c.Provider == "" {
		if t.Provider != "" {
			c.Provider = t.Provider
		}
	}
	// Debug: TOML sets a default, env/flag can override.
	if t.Debug && !c.Debug {
		c.Debug = true
	}
}

// EnvOverrideConfig applies env vars to Config. This is
// called after TOML merge so env vars always win.
func EnvOverrideConfig(c *Config) {
	if v := os.Getenv("SUPERCLI_LLM_MODEL"); v != "" {
		c.Model = v
	}
	if v := os.Getenv("SUPERCLI_LLM_PROVIDER"); v != "" {
		c.Provider = v
	}
	if v := os.Getenv("SUPERCLI_LLM_API_KEY"); v != "" {
		c.APIKey = v
	} else if v := os.Getenv("OPENAI_API_KEY"); v != "" && c.APIKey == "" {
		// Standard OpenAI env var as a fallback when nothing
		// else configured a key (config.toml providers win).
		c.APIKey = v
	}
	if v := os.Getenv("SUPERCLI_LLM_BASE_URL"); v != "" {
		c.BaseURL = strings.TrimRight(v, "/")
	}
}

// TomlConfigToEnv converts TOML defaults to env vars
// for backward compatibility with existing Load() calls.
// Only sets env vars that are NOT already set.
//
// When DefaultProvider is set, it looks up the matching
// [[providers]] entry to resolve type, base_url, and api_key.
func TomlConfigToEnv(t TomlConfig) {
	setEnvUnless("SUPERCLI_LLM_MODEL", t.DefaultModel)

	// If DefaultProvider names a configured provider, use its
	// settings. Otherwise fall back to the top-level provider.
	if t.DefaultProvider != "" {
		for _, p := range t.Providers {
			if p.Name == t.DefaultProvider {
				setEnvUnless("SUPERCLI_LLM_PROVIDER", p.Type)
				if p.BaseURL != "" {
					setEnvUnless("SUPERCLI_LLM_BASE_URL", p.BaseURL)
				}
				if p.APIKey != "" {
					setEnvUnless("SUPERCLI_LLM_API_KEY", p.APIKey)
				}
				break
			}
		}
	} else if t.Provider != "" {
		setEnvUnless("SUPERCLI_LLM_PROVIDER", t.Provider)
	}
	if t.Debug {
		setEnvUnless("SUPERCLI_DEBUG", "1")
	}
	if t.AllowAll {
		setEnvUnless("SUPERCLI_ALLOW_ALL", "1")
	}
}

func setEnvUnless(key, val string) {
	if val == "" {
		return
	}
	if _, ok := os.LookupEnv(key); !ok {
		os.Setenv(key, val)
	}
}
