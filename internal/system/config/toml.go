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
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// TomlConfig is the on-disk TOML structure. All fields are
// optional — missing fields inherit from the next lower layer.
type TomlConfig struct {
	// DefaultModel is the model id (e.g. "gpt-4o").
	DefaultModel string `toml:"default_model"`

	// DefaultProvider is the name of the provider to use
	// (matches a [[providers]].name entry). When set,
	// the provider's type, base_url, and api_key are
	// used on startup. This is the persistent equivalent
	// of selecting a provider+model in the TUI.
	DefaultProvider string `toml:"default_provider"`

	// Draft settings (F11).
	DraftMode  string `toml:"draft_mode"`
	DraftModel string `toml:"draft_model"`

	// Credit budgets (F7).
	MaxCreditsPerSession int64 `toml:"max_credits_per_session"`
	MaxCreditsPerDay     int64 `toml:"max_credits_per_day"`

	// UI.
	NoColor bool `toml:"no_color"`

	// Provider configuration.
	Provider  string         `toml:"provider"`
	Providers []ProviderConf `toml:"providers"`

	// Model price overrides (F28 source="user").
	ModelPrices []ModelPriceConf `toml:"model_prices"`

	// HiddenModels lists model IDs that the user has disabled
	// in the TUI. They are hidden from the global /models view
	// but still visible in the per-provider model list.
	HiddenModels []string `toml:"hidden_models"`

	// ReasoningEffort is the default reasoning-effort level for
	// OpenAI-family reasoning models (none|minimal|low|medium|
	// high|xhigh). Empty = provider default. Set via /reasoning.
	ReasoningEffort string `toml:"reasoning_effort"`

	// Agent.
	MaxSteps int `toml:"max_steps"`

	// ReflectEvery controls the F5.a mid-run reflection
	// checkpoint interval (every N agent steps). 0 = use
	// the built-in default; negative disables reflection.
	ReflectEvery int `toml:"reflect_every"`

	// ModelTiers are user overrides for the model tier
	// cascade (internal/tier): case-insensitive glob on the
	// model name; first match wins. None ship by default.
	ModelTiers []ModelTierConf `toml:"model_tiers"`

	// SmallFullTools restores the full always-on tool set
	// for small-tier models (escape hatch; default false =
	// small models get the trimmed core tool set).
	SmallFullTools bool `toml:"small_full_tools"`

	// ContextWindow overrides the model's context window
	// (tokens) for auto-compaction. 0 = resolve from
	// provider metadata / learned limits / default.
	ContextWindow int `toml:"context_window"`

	// Debug logging.
	Debug bool `toml:"debug"`

	// AllowAll grants full filesystem access, skipping
	// the sandbox boundary check. File operations can
	// reach any directory (sensitive system paths are
	// still blocked). Same as --allow-all flag.
	AllowAll bool `toml:"allow_all"`

	// CodexAuth configures ChatGPT-subscription ("Codex")
	// OAuth login. All fields optional; compiled-in defaults
	// match the OpenAI Codex CLI reference values.
	CodexAuth CodexAuthConf `toml:"codex_auth"`

	// WebSearch configures the web_search tool. Optional;
	// the default engine (duckduckgo) needs no API key.
	WebSearch WebSearchConf `toml:"web_search"`

	// Council configures the /council roster (F12).
	// Empty = fall back to the auto-assembled
	// cheapest-N council.
	Council CouncilConf `toml:"council"`

	// Mcp configures external MCP servers. Each
	// [mcp.servers.<name>] section spawns a stdio MCP server
	// whose tools are registered as mcp_<name>_<tool>
	// (discoverable via tool_search, not always-on).
	Mcp McpConf `toml:"mcp"`
}

// McpConf is the [mcp] section of config.toml.
type McpConf struct {
	Servers map[string]McpServerConf `toml:"servers"`
}

// McpServerConf is one [mcp.servers.<name>] entry.
//
// Example:
//
//	[mcp.servers.context7]
//	command = "npx"
//	args = ["-y", "@upstash/context7-mcp"]
//	[mcp.servers.context7.env]
//	CONTEXT7_API_KEY = "..."
type McpServerConf struct {
	Command string            `toml:"command"`
	Args    []string          `toml:"args"`
	Env     map[string]string `toml:"env"`
}

// CouncilConf is the [council] section of config.toml.
// Models is the saved roster for /council: entries are
// "providerName/modelID" (preferred) or bare model IDs.
// The /council picker overwrites this with the user's
// last selection, so it doubles as the persisted default.
type CouncilConf struct {
	Models []string `toml:"models"`
}

// WebSearchConf is the [web_search] section of config.toml.
// Engine: "duckduckgo" (default, no key), "brave", or "tavily".
// APIKey is required for brave/tavily; env vars BRAVE_API_KEY /
// TAVILY_API_KEY are used as fallbacks by main.go.
type WebSearchConf struct {
	Engine string `toml:"engine"`
	APIKey string `toml:"api_key"`
}

// CodexAuthConf is the [codex_auth] section of config.toml.
// Empty fields fall back to the compiled-in defaults
// (codexauth.DefaultClientID / DefaultIssuer / DefaultBackendURL).
type CodexAuthConf struct {
	// ClientID is the OAuth public client id.
	ClientID string `toml:"client_id"`
	// Issuer hosts /oauth/authorize and /oauth/token
	// (default https://auth.openai.com).
	Issuer string `toml:"issuer"`
	// BackendURL is the ChatGPT backend Codex API root
	// (default https://chatgpt.com/backend-api/codex).
	BackendURL string `toml:"backend_url"`
}

// ProviderConf is a named provider entry in config.toml.
type ProviderConf struct {
	Name    string `toml:"name"`
	Type    string `toml:"type"` // "openai", "anthropic", "opencode", "echo"
	BaseURL string `toml:"base_url"`
	APIKey  string `toml:"api_key"`
	Model   string `toml:"model"`
}

// ModelTierConf is a [[model_tiers]] entry: a glob pattern on
// the model name plus the tier ("small" or "big") it forces.
type ModelTierConf struct {
	Pattern string `toml:"pattern"`
	Tier    string `toml:"tier"`
}

// ModelPriceConf allows manual price overrides.
type ModelPriceConf struct {
	Model      string  `toml:"model"`
	InputCost  float64 `toml:"input_cost"`  // per 1M tokens
	OutputCost float64 `toml:"output_cost"` // per 1M tokens
}

// LoadToml reads a config.toml at the given path. Returns
// an empty TomlConfig on missing file (not an error).
func LoadToml(path string) (TomlConfig, error) {
	var cfg TomlConfig
	if path == "" {
		return cfg, nil
	}
	if _, err := os.Stat(path); err != nil {
		return cfg, nil // missing file (or directory) is OK
	}
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return cfg, fmt.Errorf("config: load %s: %w", path, err)
	}
	return cfg, nil
}

// sanitizeAPIKey strips control characters (NUL, CR, LF, ...)
// and surrounding whitespace from an API key. Keys corrupted by
// terminal paste artifacts (e.g. UTF-16 clipboard NUL padding)
// must never be persisted: a NUL-prefixed key is silently invalid
// for HTTP Authorization headers and survives every config
// round-trip (SaveActiveConfig, SetPrice, hidden-models saves).
func sanitizeAPIKey(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// SaveToml writes a TomlConfig to path, creating directories.
// Provider API keys are sanitized so paste artifacts (NULs,
// CR/LF) can never be persisted to disk.
func SaveToml(path string, cfg TomlConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	for i := range cfg.Providers {
		cfg.Providers[i].APIKey = sanitizeAPIKey(cfg.Providers[i].APIKey)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := toml.NewEncoder(f)
	enc.Indent = ""
	return enc.Encode(cfg)
}

// FindTomlPaths returns the global and project config.toml paths.
// dataDir is the resolved SuperCli data directory (portable default:
// supercli-data next to the executable); the global config lives
// directly inside it. The project config stays a per-workspace
// override at <cwd>/.supercli/config.toml.
func FindTomlPaths(dataDir, cwd string) (global, project string) {
	global = filepath.Join(dataDir, "config.toml")
	if cwd != "" {
		p := filepath.Join(cwd, ".supercli", "config.toml")
		if filepath.Clean(p) != filepath.Clean(global) {
			project = p
		}
	}
	return
}

// mergeToml applies non-zero values from `src` onto `dst`.
// src takes precedence; zero/empty values in src are skipped.
func mergeToml(dst *TomlConfig, src TomlConfig) {
	if src.DefaultModel != "" {
		dst.DefaultModel = src.DefaultModel
	}
	if src.DefaultProvider != "" {
		dst.DefaultProvider = src.DefaultProvider
	}
	if src.DraftMode != "" {
		dst.DraftMode = src.DraftMode
	}
	if src.DraftModel != "" {
		dst.DraftModel = src.DraftModel
	}
	if src.MaxCreditsPerSession != 0 {
		dst.MaxCreditsPerSession = src.MaxCreditsPerSession
	}
	if src.MaxCreditsPerDay != 0 {
		dst.MaxCreditsPerDay = src.MaxCreditsPerDay
	}
	if src.Provider != "" {
		dst.Provider = src.Provider
	}
	if src.Debug {
		dst.Debug = true
	}
	// NoColor: only override if explicitly set to true
	// (we can't distinguish "not set" from "false" with
	// bool in TOML, so we use a pointer approach in the
	// raw decode — but for simplicity, we merge all bools).
	// In practice: project config can set no_color = true.
	if src.NoColor {
		dst.NoColor = true
	}
	if src.ReasoningEffort != "" {
		dst.ReasoningEffort = src.ReasoningEffort
	}
	if src.MaxSteps > 0 {
		dst.MaxSteps = src.MaxSteps
	}
	if src.ReflectEvery != 0 {
		dst.ReflectEvery = src.ReflectEvery
	}
	// Model tier rules: project overrides global entirely
	// (same semantics as Providers).
	if len(src.ModelTiers) > 0 {
		dst.ModelTiers = src.ModelTiers
	}
	if src.SmallFullTools {
		dst.SmallFullTools = true
	}
	if src.ContextWindow > 0 {
		dst.ContextWindow = src.ContextWindow
	}
	// Providers list: project overrides global entirely.
	if len(src.Providers) > 0 {
		dst.Providers = src.Providers
	}
	// Model prices: append (user may add more).
	if len(src.ModelPrices) > 0 {
		dst.ModelPrices = src.ModelPrices
	}
	// Codex auth: field-wise override.
	if src.CodexAuth.ClientID != "" {
		dst.CodexAuth.ClientID = src.CodexAuth.ClientID
	}
	if src.CodexAuth.Issuer != "" {
		dst.CodexAuth.Issuer = src.CodexAuth.Issuer
	}
	if src.CodexAuth.BackendURL != "" {
		dst.CodexAuth.BackendURL = src.CodexAuth.BackendURL
	}
	// Council roster: project overrides global entirely
	// (same semantics as Providers).
	if len(src.Council.Models) > 0 {
		dst.Council.Models = src.Council.Models
	}
	// Web search: field-wise override.
	if src.WebSearch.Engine != "" {
		dst.WebSearch.Engine = src.WebSearch.Engine
	}
	if src.WebSearch.APIKey != "" {
		dst.WebSearch.APIKey = src.WebSearch.APIKey
	}
	// MCP servers: merged per name; a project entry overrides
	// the global entry with the same name.
	if len(src.Mcp.Servers) > 0 {
		if dst.Mcp.Servers == nil {
			dst.Mcp.Servers = make(map[string]McpServerConf, len(src.Mcp.Servers))
		}
		for name, s := range src.Mcp.Servers {
			dst.Mcp.Servers[name] = s
		}
	}
}

// ResolveConfig builds the final config by merging layers.
// Hierarchy: global TOML < project TOML < env vars < flags.
// Returns the merged TomlConfig.
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
