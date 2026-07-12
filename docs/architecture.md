# Architecture — the agent loop end-to-end

One user turn, coordinator route:

```text
user prompt
   │
   ▼
[route]  navigator: keyword map first, model only when ambiguous
   │        chat / advisor → tiny context (2 tools)   coordinator → full loop
   ▼
[context defense]  estimator → prune (zero-LLM) → summary fallback
   ▼
[provider call]  stable tools list · leading system block byte-stable ·
                 volatile text (time stamp, reminders) demoted to the tail
   ▼
[tool calls]  execute, append results (append-only), repeat ≤ MaxSteps
   ▼
answer + telemetry line (cache | eval | gen)
```

Everything below explains one stage: What / How / Why, with the measured
numbers that forced each decision. Main file: `internal/agent/loop.go`.

## Routing (navigator, keyword-first)

**What.** Every user prompt is classified into a route before any big
context is built: `coordinator` (full agent), `chat` / `advisor` (tiny
context: only `tool_search` + `recall` as tools), `clarify`.

**How.** `internal/agent/routing.go`. A data-shaped keyword map
(`RouteMap.ClassifyConfident`) decides first; only when it is not
confident does the navigator *model* get a round-trip (mode `auto`,
the default — `navigator = "on"|"off"` forces always/never). Workers
never route: delegation is always coordinator-shaped.

**Why.** Smalltalk should not pay for the full tool prompt. The keyword
map is free; the model round-trip is reserved for genuinely ambiguous
prompts (advisor vs coordinator), which keywords cannot judge. Chat-only
history uses a growing append-only window (`chatWindowMaxTokens`,
loop.go) — trimmed rarely and in big jumps, because a per-turn trim would
make the prompt non-append-only and cap KV reuse by construction.

## Thin tool protocol + stableToolset

**What.** On the coordinator route only a small **schema-core** of tools
carries a full JSON Schema every turn (`thinCoreTools`, routing.go:
tool_search, edit_line, read_context, read_lines, ctx_execute, recall,
list_dir). Every other tool is advertised as a one-line name+hint in a
compact catalog and activated on demand via `tool_search`, which returns
the full schema as its result text.

**How.** `thinPartition` (loop.go) is the single source of truth for the
core/tail split; `buildToolDefs`, the catalog preamble and the /context
accounting all derive from it. Hints are capped at 80 runes
(`defaultThinHintMax`) — measured ~84% token saving vs full schemas while
staying readable. Individual schemas are also audited for weight (e.g.
commit 6ed5e1e trimmed ctx_execute ~539→344 tokens; it alone was ~41% of
the schema-core).

**Why stableToolset (default ON).** Chat templates serialize the `tools`
list at the very start of the prompt. Promoting a tool_search-activated
tool into the schema set would change those first bytes and kill the
entire server-side KV cache. With `stable_toolset` the request `tools`
list stays byte-identical all session: activated tools remain in the
tail, their schema having already reached the model as tool_search result
text, and `Registry.Execute` dispatches by name, not by promotion.
Live-confirmed on qwen3.5-9b; this was the last remaining cache-killer in
the toolset path.

**Lesson pinned in code (the "list_dir lesson").** A tool that is the
mode's *primary action* must be schema-carrying core, never catalog tail:
in the tail, the catalog framing ("call tool_search to load it") made
small models waste a tool_search round-trip just to list a directory.
Same reasoning puts `task` in the orchestrator core (orchestrator.go).

## System-message demote (the stable prompt front)

**What.** Only the *leading* run of system messages stays as
`RoleSystem`. Every system message injected mid-conversation (the
per-request freshness stamp, thin-tools preamble, reflection checkpoints,
compaction summaries) is re-rendered in place as a user message wrapped
in `<system-reminder>` tags, merged with adjacent user messages so strict
alternating templates never see user,user.

**How.** `internal/llm/system_demote.go`, applied on both the OpenAI and
Anthropic build paths (and the codex/chat-only paths, commit 16df160).
Anti-hoist regressions are pinned by `system_demote_test.go` and
`cache_prefix_test.go`.

**Why (the hoist bug, measured 2026-07-05).** Providers used to coalesce
all system text into one leading block, which moved the minute-granular
time stamp to the *front* of the prompt. Result: every minute tick
invalidated the whole KV cache. Live on Qwen3.5-9B: a minute tick inside
a session cost a full re-eval, `cache_n=0, eval=2216` for ~173 tokens of
new content (13× overpay); after the demote fix the same tick kept the
cache, `cache_n=2014, eval=371` — the level of an ordinary step. Bonus:
a new session's first request now reuses the tools+system prefix of the
previous session. Scratchpad log: cache-hunt-2026-07-05.

## Context defense: estimator → prune → summary

Three layers, cheapest first. Order matters: each layer exists to keep
the next (more expensive) one from firing.

### 1. Calibrated token estimator

`internal/llm/estimate.go` — `nonws/3 + 16/msg`. Calibrated against live
llama-server `prompt_n` (Qwen3.5-9B, 27 requests, 2026-07-05): the old
`len/4` heuristic **underestimated by 23–32%**, so compaction fired too
late; `nonws/3+16` lands at mean 0.95 of actual (range 0.86–1.02). It
also counts tool-call arguments, which the old helpers ignored entirely
(a big write_file was invisible to the trigger). For cl100k cloud models
it slightly overestimates — the safe direction for a trigger. One
estimator is shared by the loop, /context, resume and the chat window,
so a calibration fix lands everywhere at once.

### 2. Zero-LLM tool-result prune

`internal/agent/prune.go`. Above 60% of the window, old `RoleTool`
results are replaced in place by a short marker ("re-run X with the same
arguments if needed"); the assistant's tool call (name+args) is never
touched, so the model can re-fetch. KV-cache rules baked into the
implementation: prune **rarely and in one big batch** (only when ≥25% of
the estimate is reclaimable — one cache re-eval, big payoff), never
rewrite a marker twice (append-only between prunes), always keep the
freshest 8k tokens of results plus the whole current step.

Measured (2026-07-05, window forced to 8k, five ~6k-token reads): prompt
held at ~8.5–9k instead of ~31k, 15.3k estimated tokens reclaimed, and
after the prunes the history went append-only again — next request hit
77% cache. The summary fallback never fired. Scratchpad:
compaction-2026-07-05.

### 3. Summary fallback

`internal/agent/window.go` + `compact.go`. Above 80% of the window
(`autoCompactThreshold`) the pre-turn history is summarized by the model
(template + exact facts) and replaced by one system message. The cut is
at the **last user turn boundary** (`compactSplit`): the current turn
survives verbatim, because a small model resumes far better from its own
recent messages than from a summary of them. A single giant turn (> half
the window) falls back to replace-all. If summarization itself fails,
the loop hides all but the last user turn — compaction can never wedge
the session. Originals always stay in the SQLite session store
(searchable via search_history). Prune/compact also save a separate
provider-visible projection; they never rewrite the transcript.

### Durable model-context projection

The session database deliberately stores two views. `messages` is the
lossless transcript used by the UI, export and FTS search. The optional
`session_context_projections` row stores the exact conversation body the
model should see after `/clear`, `hide_messages`, budget eviction,
tool-result pruning or compaction, together with the highest transcript
sequence covered by that snapshot.

On `/resume` and every new Web GUI request, `ReadModelContext` loads the
snapshot and appends transcript rows newer than its boundary. Thus new
messages need no snapshot rewrite, while removed context cannot silently
return after a restart. Missing/corrupt projection data fails open to the
full transcript. Leading system messages are not snapshotted because a
new loop rebuilds them from the current configuration.

### Window resolution

`Loop.window()`: config `context_window` > provider metadata > learned
limit > 16384 default. Provider "context length exceeded" errors are
parsed to *learn* the real limit (`handleContextOverflow`), then compact
and retry once.

## Loop guardrails

- `MaxSteps` bounds tool-call iterations per run.
- Tool results are size-capped at the tool layer (byte/line caps with
  truncation markers) so a single result cannot flood the window.
- A batch of multiple `task` calls in one turn may run concurrently, but
  only on cloud backends — on a single local GPU workers serialize on one
  server slot anyway and interleaved contexts thrash the KV cache
  (`task_parallel` tri-state, auto by base URL).
- Reflection checkpoints and ultrawork reminders ride the demote path
  like everything else — they no longer invalidate the prompt front.

## Session-write reliability

Session persistence (SQLite message appends, usage counters) is
best-effort by design: a failed write never aborts inference. It is not
silent, though — a health tracker on the loop keeps the FIRST error
sticky (operation, message, time), counts every failure, and surfaces
exactly one warning per outage (a notice line in the TUI/webgui, one
stderr line in batch mode).

Failed appends are buffered in memory (up to 64 messages) and retried
in the original order before the next append, so a transient failure —
released file lock, freed disk space — leaves no hole in the on-disk
history. The store assigns `seq` at write time, which makes in-order
retry safe. If the outage outlasts the buffer, the oldest entries are
dropped and counted as lost. A successful write after an outage emits a
one-line "persistence recovered" notice.

`/status` shows the current state: ok, or the failure count, sticky
first/last error, retry-buffer depth and any overflow losses.
