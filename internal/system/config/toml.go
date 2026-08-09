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
	NoColor  bool   `toml:"no_color"`
	Language string `toml:"language"`

	// Provider configuration.
	Provider  string         `toml:"provider"`
	Providers []ProviderConf `toml:"providers"`

	// FallbackModels is an explicit, ordered main-model failover chain.
	// Entries use "providerName/model-id" to switch host/key, or a bare
	// model id to stay on the primary transport. Empty (the default) means
	// no cross-model failover and therefore can never trigger a paid model.
	FallbackModels []string `toml:"fallback_models"`
	// FallbackCooldownSeconds is how long a backend that failed before its
	// first output is skipped. Zero selects the conservative 30s default.
	FallbackCooldownSeconds int `toml:"fallback_cooldown_seconds"`

	// Sampling is the `[sampling]` table: temperature, top_p and friends.
	// Every field is optional; unset parameters are never sent, so the
	// model server keeps applying its own defaults. See SamplingConf.
	Sampling SamplingConf `toml:"sampling"`

	// Model price overrides (F28 source="user").
	ModelPrices []ModelPriceConf `toml:"model_prices"`

	// HiddenModels stores provider-scoped visibility references. Older bare
	// model IDs are migrated by providers.Manager on load.
	HiddenModels []string `toml:"hidden_models"`

	// ReasoningEffort is the default reasoning-effort level for
	// OpenAI-family reasoning models (none|minimal|low|medium|
	// high|xhigh). Empty = provider default. Set via /reasoning.
	ReasoningEffort string `toml:"reasoning_effort"`

	// Thinking toggles chain-of-thought for LOCAL models that honour
	// an in-prompt soft switch (Qwen /no_think). Tri-state: nil =
	// built-in default (thinking ON), explicit true/false overrides.
	// Set at runtime via /think on|off. Orthogonal to reasoning_effort
	// (which steers cloud reasoning models).
	Thinking *bool `toml:"thinking"`

	// Agent.
	MaxSteps int `toml:"max_steps"`
	// MaxStepsOr (below) is how consumers read it: 0 = the shared
	// built-in runaway safety net (agent.DefaultMaxSteps).

	// ReflectEvery controls F5.a mid-run reflection. 0 selects the
	// adaptive signal-driven policy, a positive value forces a fixed
	// N-step interval, and a negative value disables reflection.
	ReflectEvery int `toml:"reflect_every"`

	// ModelTiers are user overrides for the model tier
	// cascade (internal/tier): case-insensitive glob on the
	// model name; first match wins. None ship by default.
	ModelTiers []ModelTierConf `toml:"model_tiers"`

	// SmallFullTools is the full-schema escape hatch for automatic tool
	// thinning. Despite the historical name, it also disables local-host
	// thinning for a large model on a private backend.
	SmallFullTools bool `toml:"small_full_tools"`

	// Orchestrator is the three-state delegation policy. Nil is adaptive:
	// the normal coordinator has full tools and delegates when useful.
	// True is hard orchestration: the main loop gets delegation plus a
	// read-only lookup set and must delegate substantial work. False is
	// never: worker tools are absent from the main agent entirely.
	// Set via /orchestrator auto|on|off; takes effect on the next launch,
	// because
	// swapping the tool list mid-session would break the KV-cache prefix.
	Orchestrator *bool `toml:"orchestrator"`

	// OrchestratorModel optionally routes the COORDINATOR (the main loop
	// that delegates work) to a separate model or provider, mirroring
	// task_model which routes the workers. Format: "model" or
	// "provider/model" (OpenRouter-style slashes stay part of the bare
	// model id unless the prefix names a configured provider). Empty =
	// the main model also coordinates. In the two-model setup the
	// orchestrator writes the task brief and the task_model worker
	// executes it in an isolated context.
	OrchestratorModel string `toml:"orchestrator_model"`

	// DarwinParallel controls whether Darwin best-of-N spawns its
	// agents concurrently. Tri-state: nil = auto (parallel for cloud
	// backends, sequential for local ones, decided by the base URL),
	// explicit true forces parallel, explicit false forces sequential.
	// The override exists for a self-hosted server behind a public
	// address (auto would misread it as cloud) or to force sequential.
	DarwinParallel *bool `toml:"darwin_parallel"`

	// TaskParallel controls whether MULTIPLE `task` delegations emitted
	// in a single model turn run concurrently. Tri-state: nil = auto
	// (parallel for cloud backends, sequential for local ones, decided
	// by the base URL — one local GPU serializes the requests on a
	// single server slot anyway, N× wall time, and interleaved worker
	// contexts thrash each other's KV cache), explicit true forces
	// parallel, explicit false forces sequential. Forcing parallel on a
	// local backend prints a runtime warning. The override exists for a
	// self-hosted server behind a public address or a forced choice.
	TaskParallel *bool `toml:"task_parallel"`

	// CachePrompt hints local llama.cpp-family servers to reuse the KV
	// prompt cache across turns (`"cache_prompt": true` in the request).
	// Tri-state: nil = auto (sent to local/private hosts, never to cloud
	// endpoints that reject unknown fields), explicit true/false forces
	// it. Applies to providers built after startup; the active provider
	// keeps its resolved value for the session.
	CachePrompt *bool `toml:"cache_prompt"`

	// SlotCache persists llama.cpp slot KV state across SuperCli
	// sessions (POST /slots/{id}?action=save|restore), so resuming a
	// session prefills against the saved cache instead of re-evaluating
	// the whole history. Requires the server to run with
	// `--slot-save-path <dir>`. Tri-state: nil = auto (attempted on
	// local/private hosts only, silently disabled after the first
	// unsupported/failed call), explicit true forces it for a
	// self-hosted server behind a public address, explicit false turns
	// it off entirely. Cloud endpoints are never probed.
	SlotCache *bool `toml:"slot_cache"`

	// Navigator selects how the pre-request route (chat/advisor/
	// coordinator) is decided: "on" runs the navigator model every user
	// turn; "off" skips it entirely (always coordinator — safe for
	// scripted use); "auto" (default) is keyword-first and never pays the main
	// model for ambiguity (safe coordinator fallback). When the TUI
	// already has a separate task/draft provider, auto may classify there.
	// Empty = built-in default ("auto").
	Navigator string `toml:"navigator"`

	// StableToolset (thin tool protocol only) keeps the request
	// `tools` list fixed for the whole session: tools activated
	// via tool_search are NOT promoted into the schema-carrying
	// set (their schema still reaches the model as the tool_search
	// result text, and execution is by name). Chat templates
	// serialize `tools` at the very start of the prompt, so a
	// stable list preserves the server-side KV prompt cache across
	// activations. Tri-state: nil = built-in default, explicit
	// true/false overrides.
	StableToolset *bool `toml:"stable_toolset"`

	// TaskMaxSteps caps how many model turns a delegated worker (the
	// `task` tool) may take before it is stopped. 0 = built-in default
	// (the per-agent spec value, else 10). Only applied when a spec does
	// not set its own step budget.
	TaskMaxSteps int `toml:"task_max_steps"`

	// TaskModel routes delegated `task` workers to a different model
	// than the coordinator. Two forms, matching existing conventions:
	// "model-id" keeps the coordinator's transport and swaps only the
	// model (draft_model style); "providerName/model-id" resolves a
	// configured [[providers]] entry by name (council label style), so
	// the worker runs against that provider's host/key. Empty = workers
	// inherit the coordinator's provider (unchanged behaviour, zero
	// cost). When the worker backend cannot be built or is unreachable,
	// delegation falls back to the coordinator's provider with a single
	// warning line — never a hard error.
	TaskModel string `toml:"task_model"`

	// CompactModel optionally routes context summaries to a separate model or
	// host so compaction does not evict the coordinator's local KV cache. It
	// accepts the same forms as task_model: "model-id" keeps the active
	// transport, while "providerName/model-id" selects a configured provider.
	// Empty preserves the current behaviour and uses the active chat model.
	CompactModel string `toml:"compact_model"`

	// TaskMaxTokens caps a delegated worker's total token spend
	// (input+output across turns). Once a turn pushes the running total
	// past this, the worker is stopped and its partial report is
	// returned with a failed status. 0 = no token cap.
	TaskMaxTokens int64 `toml:"task_max_tokens"`

	// DraftVerify turns on the draft-verify ladder for delegated `task`
	// work that changes files: the worker DRAFTS the change (on task_model
	// when set, else the coordinator's host), an objective sieve
	// (verify_commands) runs for free, then the coordinator's big model
	// issues a verdict on the DIFF and the sieve evidence — accept, revise
	// (bounded rounds), or take over. Tri-state: nil = built-in default
	// (OFF, byte-identical behaviour), explicit true/false overrides. The
	// ladder is only worth it against a small task_model; a red sieve at
	// zero token cost is the main win. Set at runtime via /draftverify.
	DraftVerify *bool `toml:"draft_verify"`

	// VerifyCommands is the objective sieve run after a draft: each entry
	// is a full command line executed in the sandbox root, in order; the
	// first non-zero exit is the RED evidence. Example:
	// ["go build ./...", "go test ./..."]. Empty = the verdict runs on the
	// diff alone (weaker, but still catches nothing-changed drafts). Only
	// consulted when draft_verify is on.
	VerifyCommands []string `toml:"verify_commands"`

	// DraftVerifyMaxRounds caps REVISE round-trips before the big model
	// takes over — the anti-ping-pong bezpiecznik. 0 = built-in default (2).
	DraftVerifyMaxRounds int `toml:"draft_verify_max_rounds"`

	// NoopGate skips a batch (-p) run entirely — zero LLM calls —
	// when the working tree manifest (path+size+mtime fingerprint)
	// is identical to the one saved after the last successful run of
	// the SAME prompt. Interactive sessions are never gated (the user
	// typing to the model means they want the model). The gate fails
	// open: any missing/broken manifest just runs normally.
	// Tri-state: nil = built-in default (OFF), explicit true/false
	// overrides.
	NoopGate *bool `toml:"noop_gate"`

	// PreflightRepo appends a compact auto-collected repo-state block
	// (branch/HEAD, pending changes, recent commits, recently modified
	// files; hard token budget) to the FIRST user message of a session
	// and to every delegated worker's briefing, so the model does not
	// spend its first turns rediscovering the repo. Uses the git binary
	// when available; pure-Go mtime fallback otherwise — git is never
	// required. The block rides the variable side of the prompt, never
	// the system prefix (KV-cache safe). Tri-state: nil = built-in
	// default (ON), explicit true/false overrides.
	PreflightRepo *bool `toml:"preflight_repo"`

	// ContextWindow overrides the model's context window
	// (tokens) for auto-compaction. 0 = resolve from
	// provider metadata / learned limits / default.
	ContextWindow int `toml:"context_window"`

	// PruneProtectTokens sizes the freshest tool-result tail
	// (estimated tokens) that the zero-LLM context prune keeps
	// verbatim; older tool results are replaced in place with a
	// short marker once the context estimate crosses 60% of the
	// window AND at least 25% of it can be reclaimed in one batch.
	// 0 = scale automatically with the active model window (1/16, clamped
	// to 2048..65536). Negative disables pruning.
	PruneProtectTokens int `toml:"prune_protect_tokens"`

	// MemoryBriefingTokens caps the session-start memory briefing
	// (user preferences + recent session-journal entries) injected
	// into the system prompt. It is a HARD budget: preferences and
	// the freshest journal lines are packed until the cap is hit,
	// the rest stays in the DB for recall. 0 = built-in default
	// (700 for normal tiers, 300 for the small tier).
	MemoryBriefingTokens int `toml:"memory_briefing_tokens"`

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
// Engine: "duckduckgo" (default, no key), "brave", "tavily", or
// "searxng". BaseURL is required only for SearXNG.
// APIKey is required for brave/tavily; env vars BRAVE_API_KEY /
// TAVILY_API_KEY are used as fallbacks by main.go.
type WebSearchConf struct {
	Engine  string `toml:"engine"`
	APIKey  string `toml:"api_key"`
	BaseURL string `toml:"base_url"`
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
	Name         string   `toml:"name"`
	Type         string   `toml:"type"` // "openai", "anthropic", "opencode", "echo"
	BaseURL      string   `toml:"base_url"`
	APIKey       string   `toml:"api_key"`
	Model        string   `toml:"model"`
	CachedModels []string `toml:"cached_models,omitempty"` // last successful /models result; keeps offline servers selectable
	Disabled     bool     `toml:"disabled,omitempty"`      // keep configuration but exclude from scans/model picker
}

// ModelTierConf is a [[model_tiers]] entry: a glob pattern on
// the model name plus the tier ("small" or "big") it forces.
type ModelTierConf struct {
	Pattern string `toml:"pattern"`
	Tier    string `toml:"tier"`
}

// ModelPriceConf allows manual price overrides.
type ModelPriceConf struct {
	Provider        string  `toml:"provider,omitempty"` // empty = legacy/global
	Model           string  `toml:"model"`
	InputCost       float64 `toml:"input_cost"`        // per 1M tokens
	CachedInputCost float64 `toml:"cached_input_cost"` // per 1M tokens; 0 = unknown
	OutputCost      float64 `toml:"output_cost"`       // per 1M tokens
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
