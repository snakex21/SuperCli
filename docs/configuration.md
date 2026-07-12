# Configuration — knobs, defaults, and when to touch them

**The rule: an empty config is the best config.** Every default below is
a measured decision, pinned by `internal/app/defaults_test.go` — a change
of default must edit that test, so it can never happen by accident. Knobs
are escape hatches for unusual setups, not required tuning.

Layers (highest wins): CLI flags > env vars > project
`<cwd>/.supercli/config.toml` > global `supercli-data/config.toml`.
Struct of record: `internal/system/config/toml.go` (field comments there
are the full reference; this table is the tour).

Tri-state knobs (`*bool` in Go, plain `true`/`false` in TOML) distinguish
"unset = built-in/auto" from an explicit override.

## Core

| knob | default | touch when |
|---|---|---|
| `default_model`, `default_provider`, `[[providers]]` | — | initial setup; usually written by the TUI menus |
| `thinking` | unset = **ON** | never as an "optimization" — models without chain-of-thought are worse; `/think off` is a conscious opt-out for local soft-switch models (Qwen `/no_think`) |
| `reasoning_effort` | provider default | steering cloud reasoning models; `/reasoning` |
| `max_steps` | built-in | runaway loops in exotic setups |
| `context_window` | 0 = auto (provider metadata > learned > 16384) | the provider lies about its window |

## Cache / prompt shape (see architecture.md, performance.md)

| knob | default | touch when |
|---|---|---|
| `stable_toolset` | unset = **ON** | debugging only; OFF re-enables the tools-list cache-killer |
| `cache_prompt` | unset = auto (local hosts only) | self-hosted server behind a public address (force on) |
| `slot_cache` | unset = auto (local hosts only; first failure = permanent off) | same as above, or `false` to stop slot files being written |
| `prune_protect_tokens` | 0 = 8192 | shrink for tiny windows; negative disables pruning entirely |
| `memory_briefing_tokens` | 0 = 700 (300 small tier) | briefing crowds a tiny window |
| `navigator` | unset = `auto` (keyword-first, model on ambiguity) | `off` for scripted use (always coordinator), `on` to always ask the model |

## Turn economy

| knob | default | touch when |
|---|---|---|
| `preflight_repo` | unset = **ON** (~73 tok → −33% turns, measured) | huge non-repo directories where the mtime walk is slow, or privacy |
| `noop_gate` | unset = **OFF** | ON only for idempotent batch pipelines — it changes answer semantics for repeated identical question-prompts (see performance.md) |

## Delegation (see delegation.md)

| knob | default | touch when |
|---|---|---|
| `orchestrator` | unset = **OFF** | opt-in working mode: long sessions of delegable work; new-session only |
| `task_model` | empty = workers inherit coordinator | you have a second (smaller/cheaper) host or model for workers |
| `task_max_steps` / `task_max_tokens` | 0 = spec-or-10 / no cap | runaway or expensive workers |
| `task_parallel` | unset = auto (cloud parallel, local sequential) | self-hosted server behind a public address; forcing parallel on one local GPU warns (slot serialization + KV thrash) |
| `draft_verify` | unset = **OFF** | only with a small `task_model` AND real `verify_commands` — otherwise the cost asymmetry that justifies it disappears |
| `verify_commands` | empty | set to the project's objective sieve, e.g. `["go build ./...", "go test ./..."]` |
| `draft_verify_max_rounds` | 0 = 2 | rarely; it is the anti-ping-pong cap |
| `darwin_parallel` | unset = auto by host | same logic as task_parallel |

## Misc

| knob | default | touch when |
|---|---|---|
| `draft_mode` / `draft_model` | off / empty | opt-in speculative drafting (F11) |
| `reflect_every` | 0 = adaptive | positive forces a fixed N-step interval; negative disables reflection. Adaptive mode runs only after two consecutive failing tool batches, an identical repeated tool-call batch, or when one useful step remains before `MaxSteps` |
| `model_tiers` | none | force a model into the small/big tier cascade |
| `small_full_tools` | false | escape hatch: full always-on toolset for small-tier models |
| `max_credits_per_session` / `_day` | 0 = uncapped | budget enforcement |
| `allow_all` | false | disables the sandbox boundary — know why |
| `[mcp.servers.*]` | none | external stdio MCP servers (tools land behind tool_search) |
| `[council]`, `[web_search]`, `[codex_auth]` | see toml.go | feature-specific |

## For agents changing defaults

1. Edit the resolver (`internal/app/*.go`) *and*
   `internal/app/defaults_test.go` — the test failing is the mechanism
   working.
2. Zero-value sentinels are contracts: `0` means "resolve the built-in at
   point of use" (e.g. `prune_protect_tokens` 0 → 8192). Never write a
   resolved default back into the struct.
3. Host-gated autos (`cache_prompt`, `slot_cache`, `*_parallel`) must
   stay `nil` by default: the decision is per base URL, and cloud
   endpoints must never be probed with local-only features.
