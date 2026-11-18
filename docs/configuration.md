# Configuration — knobs, defaults, and when to touch them

## Claude account login

In the GUI, open **Providers → Add → Claude → Sign in with Claude**. In the
terminal, select **Claude** from `/providers` or run `/login claude`. Finish
the login in the browser, then choose an available model in **Models** or
`/model`. The success screen also offers a model selector and **Chat with
Claude**, which selects that model and opens the chat. The browser confirmation
has a **Return to SuperCli** button and appears after credentials are saved.

OAuth credentials are stored only in `supercli-data/claude-auth.json`, beside
the application. Access tokens refresh automatically. Use the Claude provider's
**Edit → Sign out of Claude** action in the GUI, or `/logout claude` in the
terminal, to remove the saved credentials. Moving the application folder moves
the login with it. Login waits for the browser callback without status polling.

The separate **Anthropic** preset continues to accept API keys. The Claude
account preset sends OAuth credentials only to `https://api.anthropic.com/v1`;
it cannot be redirected to a custom proxy. Model discovery uses the account's
native catalog and reports upstream errors instead of inventing model choices.

For Claude and ChatGPT account connections, the cost area shows remaining
subscription usage instead of estimating a separate API token charge. Each
account keeps its own observed periods and reset times; remaining percentages
are never combined across accounts or periods. Detailed subscription usage is
available in **Usage** and **Accounts**, with **Refresh usage** for a manual
refresh. Claude also reads usage headers from real responses without making an
extra request. The terminal's usage tabs provide the same saved Claude windows.

Claude usage snapshots stay in `supercli-data/claude-usage.json` and are bound
to the saved login. Missing or expired observations are shown as unavailable or
stale, rather than assuming the subscription has refilled. Signing out removes
both the credentials and that account's saved usage snapshot.

## Defaults and configuration layers

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

Prompt-processing performance needs no knob. SuperCli learns a separate
prefill budget for each configured provider connection + model from TTFT and
cache telemetry, stored portably in `supercli-data/prefill-profiles.json`.

## Core

| knob | default | touch when |
|---|---|---|
| `language` | detected once from the OS (`en` fallback) | switch the desktop app and TUI together among the 27 [interface languages](../README.md) |
| `default_model`, `default_provider`, `[[providers]]` | — | initial setup; usually written by the TUI menus |
| `thinking` | unset = **ON** | never as an "optimization" — models without chain-of-thought are worse; `/think off` is a conscious opt-out for local soft-switch models (Qwen `/no_think`) |
| `reasoning_effort` | provider default | `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`; `/reasoning max` or GUI/TUI model controls |
| `openai_reasoning_responses` | unset = **ON** | an `openai`-type provider on `https://api.openai.com` sends official reasoning models (`gpt-5*`, `o1`/`o3`/`o4*`, `codex*`) through the Responses API, the only OpenAI API that keeps the selected reasoning level together with tools. `false` (or `SUPERCLI_OPENAI_REASONING_RESPONSES=0`) keeps Chat Completions; gateways, Azure, local servers and older chat models always use Chat Completions |
| `discard_previous_reasoning` | unset = **OFF** (keep history) | optionally omit completed replies' reasoning from future requests; `/reasoning-history keep/drop/default` |
| `max_steps` | 0 = built-in 300 on every surface | a runaway guard, not a work budget: normal work never reaches it, and the loop no longer ends a turn on a step count. A real loop (the same call with the same arguments) is answered with an injected error and work continues; only ~50 identical repeats in a row stop the run. An explicit positive value is a strict cap. |
| `context_window` | 0 = auto (loaded size from a local server: LM Studio `/api/v1/models`, llama.cpp `/props`, Ollama `/api/ps`; then provider metadata/catalog > learned > 16384) | only as a global fallback when several models share the same hard ceiling |
| `fallback_models` | empty = **OFF** | explicit local-to-cloud continuity, e.g. `["cloud/gpt-5-mini", "cloud/gpt-5"]`; the list itself is consent to call those backends |
| `fallback_cooldown_seconds` | 0 = 30 | a remote/local host flaps and should be retried sooner or later |
| `max_retries` | unset = **5** | how many times a temporary provider error is retried before it is shown: `0` = never, any positive number, or `-1` (also `"inf"` / `"infinite"`) = until it works or you stop the run. Invalid values (`-5`, `"abc"`) fall back to 5 with a logged warning |
| `retry_unknown_errors` | unset = **OFF** | "ignore errors": also retry provider errors that SuperCli does not recognize, within `max_retries`. Auth (401/403), invalid request (400), unknown model (404) and context overflow are still never retried |
| `requests_per_minute` | 0 = **OFF** | client-side limit of request starts per minute to each provider endpoint, shared by all parallel workers; `[[providers]].requests_per_minute` overrides it for one entry |

`fallback_models` is ordered and safe-before-output only. SuperCli tries the
selected model first, skips a backend temporarily after an early connection or
stream error, and may try the next configured entry only if no content or tool
call reached the conversation. Once a response starts, an error is returned
normally rather than risking duplicated output. With the default empty list no
fallback provider is constructed and no paid backend can be called implicitly.

```toml
fallback_models = ["cloud/gpt-5-mini", "cloud/gpt-5"]
fallback_cooldown_seconds = 30

[[providers]]
name = "cloud"
type = "openai"
base_url = "https://api.example.com/v1"
api_key = "..."
```

### Retries and request limits

Temporary provider failures are retried with one shared policy on every
surface (TUI, `--batch`, desktop/web GUI and the NestCafe embedded engine,
which all read the same `config.toml` through the shared runtime):

- **What is retried:** HTTP 408/425/429/500/502/503/504/52x/529, a gateway's
  "model temporarily unavailable" 400/422, in-stream provider errors that
  report a rate limit, overload or temporary unavailability (for example
  Cloudflare Workers AI `rate limiting: inference request per min rate
  reached`, Anthropic `overloaded_error`, OpenRouter `code: 429`), dropped
  connections, timeouts and short network outages. The provider's error code
  is used first, case-insensitive text patterns second.
- **Never retried:** 400 invalid request, 401/403 auth, 404 unknown model,
  context-window overflow, quota/billing errors and anything else classified
  as permanent — regardless of `max_retries` and `retry_unknown_errors`. The
  one-shot repairs (401 token refresh, reasoning-effort learning, retry
  without images) keep working as before and do not use the retry budget.
- **How long it waits:** the provider's `Retry-After` (seconds or HTTP date)
  when present, otherwise exponential backoff with jitter (0.5 s, 1 s, 2 s, …
  for HTTP errors; 1 s, 2 s, 4 s, … for connection failures), every single wait
  capped at 60 s. A 429 whose `Retry-After` is longer than 60 s is a quota
  window: it is shown at once so `fallback_models` can take over. Without
  Retry-After, an error body's `retry_after` is used; an error that names a
  per-minute limit backs off from 25 s so the retries reach the next minute.
- **Status:** each wait shows a localized line such as
  `Ponawianie 3/5 za 8 s (503)` / `retry 12/∞`.
- **Stopping:** with `max_retries = -1` SuperCli keeps retrying until the
  request succeeds or you stop it. Every wait ends immediately on Ctrl+C / Esc
  in the TUI, the Stop button in the GUI, or the signal in batch mode.
- **Partial streams:** an in-stream error is retried only when the failed
  attempt produced no tool call and no visible answer text, so nothing was
  executed and nothing already shown is duplicated (reasoning-only progress
  is re-requested).
- **Dropped and silent streams:** a connection that is reset or closed, or
  ends with an unexpected EOF before anything was shown, is retried on both
  the Chat Completions and the Responses wire. A stream that sends no data for
  the idle timeout ends with `no data from provider for Ns (idle timeout)` and
  is retried the same way. On the Responses wire an HTTP error keeps its status
  and `Retry-After`, so `fallback_models` cooldowns work there too.

Guards on what the model streams:

- **Output cap:** when `SUPERCLI_LLM_MAX_TOKENS` is unset, cloud endpoints get
  `max_tokens` / `max_output_tokens` = 32000 (64000 for reasoning models, whose
  budget includes hidden reasoning). Local servers, OpenCode Zen and the
  ChatGPT Codex backend get no default. If a provider rejects the default
  (400/422 naming its limit), the request is repeated once with that limit or
  without the field, and the choice is remembered for that endpoint and model.
  `SUPERCLI_LLM_MAX_TOKENS` now applies to the Responses wire as well; a
  negative value never sends a cap.
- **Tool-call arguments:** one call's arguments are stopped at 256 KiB, or
  earlier when the last 16 KiB are a loop (fewer than 1024 distinct 16-byte
  pieces, or one block of at most 2 KiB repeated at least 8 times). The model
  is asked to retry within the existing bounded retry; the rejected text is
  not kept in the history (a short `{"_rejected": ...}` stub is stored).
  Very regular data such as a long zero-filled array can trip this; the model
  is then asked to split the write.
- **Streaming time per tool call:** 10 minutes by default
  (`SUPERCLI_LLM_TOOL_STREAM_TIMEOUT`, in seconds; 0 = off), with a progress
  notice every 30 s while a long call streams.
- **Usage of cancelled streams:** tokens of a stream that was cancelled or
  failed are counted; when the provider sent no usage they are estimated and
  marked as estimated.

`requests_per_minute` limits how many requests SuperCli itself starts per
minute (a sliding 60-second window, at most N starts in any minute). Use it for
gateways with a per-minute request limit: parallel task workers, darwin
candidates, advisor/review/compaction side calls and retries all count toward
the same provider budget, so one process can reach such a limit while the
upstream account is nowhere near its own. Waiting shows a status line and is
cancelled immediately by Stop/Ctrl+C. The per-entry value is keyed by the
entry's `base_url`, so it needs one.

```toml
max_retries = -1             # or "inf"; 0 = no retries; default 5
retry_unknown_errors = false # true = also retry unrecognized provider errors
requests_per_minute = 0      # global default for every provider; 0 = off

[[providers]]
name = "workers-ai"
type = "openai"
base_url = "https://api.cloudflare.com/client/v4/accounts/ACCOUNT/ai/v1"
api_key = "..."
requests_per_minute = 50     # this endpoint only
```

Gateway notes:

- **Cloudflare Workers AI** (`https://api.cloudflare.com/client/v4/accounts/<account>/ai/v1`,
  model ids `@cf/...`): text generation is limited to 300 requests per minute
  per model (20 per minute for models that need the Workers Paid plan). Error
  bodies use the `{"success":false,"errors":[{"code":…,"message":…}]}`
  envelope, in HTTP bodies and inside the stream; SuperCli reads it. Code 3040
  (capacity) is retried; 3036 (daily free allocation of neurons used up),
  5035 (paid plan required) and unknown/invalid model codes are not, even with
  `max_retries = -1`. Many Workers AI text models cannot call tools: when a
  model rejects the `tools` field SuperCli retries once without tools, shows a
  notice and remembers it for the session.
- **Logfare** (`https://logfare.ai/v1`, a custom OpenAI-compatible provider;
  `logfare/auto` routes to an available model): account limits are fixed UTC
  windows (documented: regular accounts 40/min, 1,000/h, 5,000/day, 6
  concurrent; new accounts 10/min and 2 concurrent for the first 24 h). The
  live limit of your account is in every response's
  `x-ratelimit-limit-requests` / `x-ratelimit-remaining-requests` /
  `x-ratelimit-reset-requests` headers (a live test in November 2026 showed
  80 per minute). Its 429 carries
  `Retry-After` and `error.retry_after`; SuperCli honours both, and a daily or
  hourly window (Retry-After over 60 s) is shown at once instead of retried.
  Clients that keep retrying through refusals are paused by Logfare, so do not
  lower the waits. Part of its catalog runs on Workers AI and upstream errors
  pass through, so `AiError: rate limiting: inference request per min rate
  reached` is the shared upstream model limit, not your account limit: it is
  retried with a per-minute backoff (12–25 s, 25–50 s, then 30–60 s), and with
  `logfare/auto` a retry can land on another model. The model list only offers
  entries that serve chat and reads each model's `chat_capabilities` (tools,
  image input). A model listed with `tools: false` does not reject tools: it
  answers without calling them (and may write unrelated text), so SuperCli
  reads the endpoint's model list once per run and sends such a model no
  tools, with a notice; use `logfare/auto` or a tools-capable model for agent
  work. Suggested: `requests_per_minute = 30` on that entry
  (`10` on a new account), `max_retries = 8` (or `-1` for unattended runs).

All three are also in TUI `/settings` (Context category) and in the GUI
Settings panel ("Verification, access and fallback"); changes apply to the next request.

After a successful provider scan, SuperCli stores only the returned model IDs
as `cached_models` inside that provider entry. This tiny portable inventory
keeps LM Studio and remote llama.cpp models visible when their server is
offline; the next successful scan refreshes it. It does not trigger a model
call or add anything to the agent prompt.

For a limit that belongs to one connection and one model, use the model palette
in WebGUI (type `100k`, `1m`, or `auto`) or `/context-limit 100k` in the TUI.
The key is the configured provider name plus the exact model ID. Therefore
`anyrouter / gpt-5.6-sol = 100k` does not change `openai / gpt-5.6-sol`, which
continues to use automatic provider/catalog detection. These values are saved
in the instance-local `model-context-windows.json`; the UI manages the file.

## Persistent memory

Memory is **on by default**. It covers everything SuperCli carries from one
conversation to the next:

| part | where it lives | used by |
|---|---|---|
| global memory (user preferences and facts, project cards) | `supercli-data/memory.db` + `memory/*.md` mirror | TUI, GUI |
| project memory (facts, decisions, session summaries, learned error patterns) | `supercli-data/projects/<key>/memory.db` | TUI, GUI |
| memory briefing in the request tail (`memory_briefing_tokens`) | built from both stores | TUI, GUI |
| `remember` / `recall` tools | both stores | TUI, GUI, workers |
| automatic saves: user facts from your own words, idle-time session summaries, raw tail on exit, project card | both stores | TUI |
| automatic saves: user facts, one conversation capsule per chat | both stores | GUI |
| cross-session recall of related earlier chats (FTS, no model call) | project memory, then `sessions.db` | GUI |
| learned error patterns injected into the prompt (`/reflect`) | project memory | TUI |

Switch it off with any of these (highest wins):

| how | scope |
|---|---|
| `--no-memory` (and `--memory` to force it on) on `supercli` or `supercli-web` | this launch only |
| `SUPERCLI_MEMORY=off` (`0`, `false`, `no`) or `on` (`1`, `true`, `yes`) | this process |
| `[memory]` `enabled = false` in `<workspace>/.supercli/config.toml` | that project only (also `true` to re-enable one project) |
| `[memory]` `enabled = false` in `supercli-data/config.toml` | everywhere |

```toml
[memory]
enabled = false   # unset = on
```

The global value is what GUI **Settings → Memory** and TUI `/settings` →
Memory edit, and what `/memory on` / `/memory off` write. `/memory status`
(TUI) and the GUI settings row show the effective state for the current project
and where it comes from (launch option, environment variable, project settings,
settings or default). While memory is off, the GUI shows a **memory off** chip
next to the model button (it opens the settings) and the TUI header shows a
permanent **memory off** badge next to the run state; on a narrow terminal the
model name is shortened first so the badge stays visible.

When memory is off:

- nothing from memory is put into the prompt (the memory briefing block stays
  in place but empty, cross-session recall is skipped);
- nothing is saved: no user facts, summaries, capsules, raw tails, project-card
  updates or learned patterns;
- `remember` and `recall` are not offered to the model, and the stable system
  prompt drops its memory hint and says that memory is off;
- `search_history` only searches and reads the **current** conversation, so
  recovering compacted parts of it still works while earlier conversations are
  out of reach (other session ids are refused);
- a session that **starts** with memory off does not open the memory databases
  at all;
- stored memory is **not deleted**. Switching memory back on restores it. To
  delete it, use **Settings → Memory → Clear AI memory** or **Data → Clear AI
  memory** (both ask for confirmation).

Timing. The GUI reads the switch for every message, so a change applies to the
next message. In the TUI `/memory off` (or the `/settings` row) applies to the
next turn: the briefing becomes empty, saves stop, and `remember`/`recall` stay
listed but refuse (the tool list is kept stable for the prompt cache). A TUI
session started with memory off needs a restart after `/memory on`, because its
memory databases were never opened. Turns made while memory was off are not
saved later.

Prompt cache. With memory on, prompts are byte-identical to 1.0.33. Turning
memory off changes the stable system prefix once (memory hint removed, short
off-note added) and, in the GUI, removes the two tool schemas, so the next
request is a provider cache miss. `context_tail_pinning` keeps working: the
memory briefing already lives in the per-request live context, so an emptied
briefing is just one changed tail, appended like any other tail change. In a
running TUI session the system prefix and the tool list do not change at all
until the next start.

Not affected: conversation history itself (`sessions.db`, `/resume`), goals,
user instructions and project instructions.

## Cache / prompt shape (see architecture.md, performance.md)

| knob | default | touch when |
|---|---|---|
| `stable_toolset` | unset = **ON** | debugging only; OFF re-enables the tools-list cache-killer |
| `context_tail_pinning` | unset = **ON** (hosted and local) | `false` restores the legacy per-request tail, which breaks the KV prefix on every step (local Qwen measurement: median 498 re-evaluated tokens per follow-up request vs 1 when pinned) |
| `cache_prompt` | unset = auto (local hosts only) | self-hosted server behind a public address (force on) |
| `slot_cache` | unset = auto (local hosts only; first failure = permanent off) | same as above, or `false` to stop slot files being written |
| `prune_protect_tokens` | 0 = 1/16 of the active window, clamped to 2048..65536 | override only for unusual tool-heavy workloads; negative disables pruning entirely |
| `compact_model` | empty = active model | route summaries to a cheaper/separate `model` or `provider/model`; failure falls back to the active model |
| `memory_briefing_tokens` | 0 = 700 (300 small tier) | briefing crowds a tiny window |
| `navigator` | unset = `auto` (zero-call keyword routing; ambiguity = coordinator) | `off` for scripted use (always coordinator), `on` to always ask the model; auto may use an already-configured side model without touching the main model |

## Turn economy

| knob | default | touch when |
|---|---|---|
| `preflight_repo` | unset = **ON** (~73 tok → −33% turns, measured) | huge non-repo directories where the mtime walk is slow, or privacy |
| `completion_check` | unset = **ON** | `false` skips the one extra pass before a Run finishes with a reply that admits unfinished work (the model is asked to do the possible rest and report only what is blocked) |
| `name_check` | unset = **ON** | `false` skips the verify-before-use lookup: at the start of a project request the code-like names it mentions (files, config keys, functions, flags) are searched in the workspace, and the ones that exist nowhere are listed for the model as a soft hint ("not found; if it's a new value, create it"): it reports a missing existing name and asks instead of inventing it or editing a look-alike. Language builtins and standard-library names (`ValueError`, `JSON.parse`), git branches and tags, output-format placeholders (`path:line`) and new values ("change X from A to **B**", "rename A to **B**", "raise X to **B**") are not checked (no model call; silent when the workspace is too large to scan quickly) |
| `compute_hint` | unset = **ON** | `false` drops the one request-tail line that asks to compute exact aggregates (counts, totals, percentiles, top-N, long sorted tables, dependency cycles) with a short `ctx_execute` script instead of tallying by hand; the line is added only to requests that ask for such results |
| `noop_gate` | unset = **OFF** | ON only for idempotent batch pipelines — it changes answer semantics for repeated identical question-prompts (see performance.md) |

## Delegation (see delegation.md)

| knob | default | touch when |
|---|---|---|
| `orchestrator` | unset = **AUTO** | unset: delegate when useful; `true`: always delegate substantial work; `false`: never expose worker tools; new-session only |
| `task_model` | empty = workers inherit coordinator | you have a second (smaller/cheaper) host or model for workers |
| `task_max_steps` / `task_max_tokens` | 0 = shared 300-step safety net / no token cap | set a stricter ceiling for runaway or expensive workers |
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
| `small_full_tools` | false | full-schema escape hatch; disables automatic thinning for both small models and large models on local/private hosts |
| `max_credits_per_session` / `_day` | 0 = uncapped | budget enforcement |
| `allow_all` | false | disables the sandbox boundary — know why |
| `[mcp.servers.*]` | none | external stdio MCP servers (lazy through `mcp_bridge`) |

Relocatable MCP packages can also be placed under
`supercli-data/mcp/<package>/manifest.toml`; see
[Portable MCP packages](portable-mcp.md). They are discovered without starting
processes and move with the whole SuperCli directory.
| `[council]`, `[web_search]`, `[codex_auth]` | see toml.go | feature-specific |

### Web search

No configuration is required: `web_search` uses DuckDuckGo by default. Search
results are deduplicated and kept in a small process-local LRU cache (64
queries; 30 minutes for current topics, 24 hours for reference queries).
Transient failures and empty responses from a configured engine fall back to
DuckDuckGo; authentication and configuration errors remain visible.

Optional keyed engine:

```toml
[web_search]
engine = "brave" # or "tavily" or "exa"
api_key = "..."  # BRAVE_API_KEY / TAVILY_API_KEY / EXA_API_KEY also work
```

Optional provider-hosted search (off by default). When enabled, searches are
answered by the provider's own web search tool (OpenAI Responses `web_search`,
or xAI's compatible API); any error or empty answer falls back to the engines
above, and the model sees the same tools and result format. It is opt-in
because every hosted search is billed by the provider and sends the query
there, while the local engines work with any model, including local ones.

```toml
[web_search]
hosted = "openai"          # "off" (default), "openai", "xai" or "auto"
# hosted_model = "gpt-5.4-nano"   # default per provider
# hosted_api_key = "..."   # default: OPENAI_API_KEY / XAI_API_KEY, then a
                           # configured provider on api.openai.com / api.x.ai
```

Optional public SearXNG instance (JSON output must be enabled):

```toml
[web_search]
engine = "searxng"
base_url = "https://search.example.com"
```

The existing tool accepts optional `freshness`, `include_domains`, and
`exclude_domains` fields. `web_search` and `web_lookup` also accept `queries`
(up to 3 extra related queries run in parallel in one call, labelled per query
with duplicate URLs removed). This adds no system-prompt block or extra model
call; the work happens only when the model invokes a search.

`web_fetch` extracts the main content of HTML pages (menus, footers, cookie
banners and link lists removed), decodes legacy charsets, reads PDF text, keeps
schema.org facts such as prices and dates, lists next-page links, and pages long
documents with `offset`. Its default window is about 7% of the active model's
context window (3,000 to 14,000 characters), so small local models are not
flooded. Page text is wrapped in markers that label it as untrusted data.

### Browser

The discoverable `browser` tool drives a headless Chrome or Edge (found on PATH
or in the standard install locations; `SUPERCLI_TEST_BROWSER` selects another
executable) for pages that need JavaScript or clicking:
`open`, `click`, `type`, `read`, `back`, `close`. All browser traffic goes
through a local proxy that refuses private and loopback addresses. The browser
uses a temporary profile, closes after 5 idle minutes and when SuperCli exits,
and on Windows runs in a job object so it cannot outlive a crashed process.

`allow_all` permits absolute file and search paths outside the active
workspace while keeping sensitive operating-system folders blocked. It
can be changed live with `/allow-all on|off` in the TUI or with the
`allow_all` switch in web settings. At startup, `--allow-all` and
`SUPERCLI_ALLOW_ALL=1` are equivalent. The boundary applies consistently
to file reads and writes, code search, `@file` mentions, images, Office
documents, and ZIP extraction targets.

### Links inside the workspace

- Junctions and symlinks inside the workspace are deliberate grants by
  whoever created them. A junction (`mklink /J ws\data D:\data`) is followed
  for reads and writes without an approval prompt and without `allow_all`,
  also when its target does not exist yet. A dangling symlink is a grant too:
  `write_file` through it creates its target. A symlink whose existing target
  is outside the workspace is resolved and refused like any outside path.
- Hard links are broken on write. `write_file`, `patch_file`, `edit_files`,
  undo and the GUI file editor replace an existing file through a temporary file and a rename,
  keeping its permissions, so a workspace file hard-linked to a file outside
  (for example a pnpm `node_modules` file linked into the global store) gets
  its own copy and the outside file never changes.
- Reserved Windows device names are refused in any path component, with any
  extension and in any letter case, also with trailing dots or spaces: `CON`,
  `PRN`, `AUX`, `NUL`, `CONIN$`, `CONOUT$`, `COM1`-`COM9`, `LPT1`-`LPT9` and
  the superscript forms `COM¹²³`/`LPT¹²³` (so `sub\nul.txt` is refused too).
  This applies with `allow_all` on as well. Other platforms have no such
  names.
- `read_zip` may extract into a junction or through one (an entry such as
  `j/file.txt` when `j` is a junction). The zip-slip protections on entry names
  stay in force: absolute paths, drive letters, `..`, alternate data streams
  (`:`), device names and anything else that is not a plain relative path are
  refused, and an existing symlink that leads outside the extraction target is
  refused.

### Approvals for risky actions

Tools declare a risk level per operation (`read`, `write_reversible`,
`destructive`, `external_side_effect`). Every non-read operation of
`thunderbird_mail` (folder create/rename/delete/compact/repair, copy, move,
import, trash, restore, purge, empty Trash, address-book and contact writes),
`outlook_mail` trash/purge, and `trash` of a path outside the workspace waits
for the user's answer. The host owns the tool's `confirm` argument: a value the
model sends is discarded, and the host sets `confirm:true` only after approval.
Ordinary edits inside the workspace are not gated.

- Web GUI: an approval card in the conversation shows the operation, account,
  folder, destination, message count with a few senders/subjects (never
  bodies) and whether it can be undone. Buttons: Approve, Deny, and Approve
  similar in this session (same tool, operation, account, folder and
  destination; expires after 30 minutes; not offered for irreversible
  operations). Esc denies. Branded overlays that render generic questions
  (NestCafe) get the same request as a question with those three options.
- TUI: the same details with `y` approve, `n`/Esc deny, `a` approve similar.
- `--batch`, SuperBot and scripts: there is nobody to ask, so the call is
  refused with a tool error telling the model that the user's approval is
  needed. Allow specific scopes in the global `<data-dir>/config.toml` only
  (project configs cannot pre-approve):

  ```toml
  [approvals]
  # tool:operation[:account[:folder[:destination]]]; "*" = any segment
  auto_approve = ["thunderbird_mail:copy:account2"]
  session_grant_minutes = 30
  ```

  The explicit `--allow-all` command-line flag of a batch run approves every
  gated action without asking. This is risky: a model can then delete mail or
  empty Trash on its own. Prefer a narrow `auto_approve` entry. `allow_all` in
  config.toml and the GUI switch keep their filesystem meaning only and never
  approve gated actions; interactive GUI/TUI runs always ask.

Each decision is appended to `<data-dir>/approvals.jsonl` (time, session,
tool, operation, scope, risk, decision; no message content).

### Thunderbird Bridge pairing

`thunderbird_mail` works through the Thunderbird AI Bridge extension (0.10.0 or
newer). SuperCli talks to it only after a one-time pairing:

1. GUI: Control panel -> Thunderbird -> Pair (TUI: `/thunderbird pair`). A
   16-character code appears, valid for 5 minutes.
2. Thunderbird: Add-ons and Themes -> Thunderbird AI Bridge -> Options, enter
   the code, click Pair. Both sides then hold the same 256-bit secret.

`/thunderbird` (or the GUI section) shows paired/connected, which instance
holds port 127.0.0.1:47831 and why an extension was refused;
`/thunderbird unpair` forgets the secret. The secret is stored in
`<data-dir>/thunderbird-bridge/pairing.json`, protected with DPAPI for the
current Windows user; a data folder copied to another account or machine must
be paired again.

Every connection is mutually authenticated (HMAC-SHA256 challenge-response) and
every request and response is signed with a timestamp and a single-use nonce.
The bridge listens on loopback only, requires the exact `Host` header, refuses
web origins and sends CORS headers only for the paired extension. An unpaired
SuperCli does not open the port; a second instance never takes it over and says
which instance holds it. Extensions older than 0.10.0 are refused with an
"update the extension" message. The protocol is described in
`docs/PROTOCOL.md` of the Thunderbird-AI-Bridge repository.

## For agents changing defaults

1. Edit the resolver (`internal/app/*.go`) *and*
   `internal/app/defaults_test.go` — the test failing is the mechanism
   working.
2. Zero-value sentinels are contracts: `0` means "resolve the built-in at
   point of use" (e.g. `prune_protect_tokens` 0 → a window-scaled tail). Never write a
   resolved default back into the struct.
3. Host-gated autos (`cache_prompt`, `slot_cache`, `*_parallel`) must
   stay `nil` by default: the decision is per base URL, and cloud
   endpoints must never be probed with local-only features.

## Previous reasoning in conversation history

The default keeps native reasoning from completed replies. To experiment with
smaller requests, use **Discard previous reasoning** / **Usuwaj wcześniejsze
myślenie** in GUI settings or TUI /settings → Models and context.

Interactive CLI commands:

    /reasoning-history drop
    /reasoning-history keep
    /reasoning-history default

With no argument, the command shows the current preference. The equivalent
top-level setting in the portable config.toml is:

    discard_previous_reasoning = true

Unset/false keeps reasoning. The preference is sampled at the beginning of each
user turn, including resumed worker turns; a setting change does not rewrite an
in-progress request sequence. It adds no model call or prompt instruction.

DROP omits native reasoning from completed assistant replies in future model
requests. Visible answers, the saved transcript and GUI/TUI thinking remain.
Turning KEEP back on makes retained history available again. Reasoning attached
to tool calls is retained because some provider protocols require it even in old
turns; active or interrupted tool exchanges are protected as well. Normal context
compaction still applies, so KEEP cannot recreate history already compacted away.

This setting does not disable generation of new thinking. Smaller input does not
guarantee a faster or cheaper whole task; the model can generate more reasoning
or repeat work. See the [paired evaluation](evals/2026-09-23-native-reasoning.md).
