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
	if src.StableToolset != nil {
		dst.StableToolset = src.StableToolset
	}
	if src.TaskMaxSteps != 0 {
		dst.TaskMaxSteps = src.TaskMaxSteps
	}
	if src.TaskMaxTokens != 0 {
		dst.TaskMaxTokens = src.TaskMaxTokens
	}
	if src.TaskModel != "" {
		dst.TaskModel = src.TaskModel
	}
	if src.CompactModel != "" {
		dst.CompactModel = src.CompactModel
	}
	if src.DraftVerify != nil {
		dst.DraftVerify = src.DraftVerify
	}
	if len(src.VerifyCommands) > 0 {
		dst.VerifyCommands = src.VerifyCommands
	}
	if src.DraftVerifyMaxRounds != 0 {
		dst.DraftVerifyMaxRounds = src.DraftVerifyMaxRounds
	}
	if src.Thinking != nil {
		dst.Thinking = src.Thinking
	}
	if src.Navigator != "" {
		dst.Navigator = src.Navigator
	}
	if src.Orchestrator != nil {
		dst.Orchestrator = src.Orchestrator
	}
	if src.DarwinParallel != nil {
		dst.DarwinParallel = src.DarwinParallel
	}
	if src.TaskParallel != nil {
		dst.TaskParallel = src.TaskParallel
	}
	if src.CachePrompt != nil {
		dst.CachePrompt = src.CachePrompt
	}
	if src.SlotCache != nil {
		dst.SlotCache = src.SlotCache
	}
	if src.NoopGate != nil {
		dst.NoopGate = src.NoopGate
	}
	if src.PreflightRepo != nil {
		dst.PreflightRepo = src.PreflightRepo
	}
	if src.ContextWindow > 0 {
		dst.ContextWindow = src.ContextWindow
	}
	if src.PruneProtectTokens != 0 {
		dst.PruneProtectTokens = src.PruneProtectTokens
	}
	if src.MemoryBriefingTokens > 0 {
		dst.MemoryBriefingTokens = src.MemoryBriefingTokens
	}
	// Providers list: project overrides global entirely.
	if len(src.Providers) > 0 {
		dst.Providers = src.Providers
	}
	if len(src.FallbackModels) > 0 {
		dst.FallbackModels = src.FallbackModels
	}
	if src.FallbackCooldownSeconds > 0 {
		dst.FallbackCooldownSeconds = src.FallbackCooldownSeconds
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
	if src.WebSearch.BaseURL != "" {
		dst.WebSearch.BaseURL = src.WebSearch.BaseURL
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

// MaxStepsOr returns the configured max_steps when positive, otherwise
// def — the caller's built-in default (TUI 10, batch and WebGUI 25).
// This keeps the documented `max_steps` knob honoured on every surface
// while an empty config still gets the tuned per-surface cap.
func (t TomlConfig) MaxStepsOr(def int) int {
	if t.MaxSteps > 0 {
		return t.MaxSteps
	}
	return def
}

// ResolveConfig builds the final config by merging layers.
// Hierarchy: global TOML < project TOML < env vars < flags.
// Returns the merged TomlConfig.
