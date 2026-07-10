# Performance — telemetry, caches, turn economy

Design rule behind all of it: **on a local server the costs are, in
order: a turn > a prompt-front re-eval > appended tokens.** Optimize in
that order, and never guess — every mechanism here shipped with a live
measurement.

## Telemetry (how the numbers are obtained)

**What.** Every turn ends with a token line: `[tokens] in=... out=...
total=... cache=... eval=...` in batch stderr (app/main.go), the same
data on the TUI/web done-event (`cache | eval | gen`, cache-hit %).

**How.** `cache` is the provider-reported cached prompt tokens
(`usage.prompt_tokens_details.cached_tokens`, which llama.cpp mirrors
from its `timings.cache_n`); `eval = in − cache` is what the server
actually prefilled. Verified token-exact against server logs during the
2026-07-05 cache hunt. This line is the ground truth used for every
number below — if you change prompt construction, watch it.

## KV-cache discipline (summary; details in architecture.md)

The prompt must be byte-stable at the front, append-only at the tail.
The three historical cache-killers and their fixes, all live-measured:

| killer | fix | measured effect |
|---|---|---|
| minute-granular time stamp hoisted to prompt front | demote mid-conversation system msgs to tail (`system_demote.go`) | minute tick: eval 2216 → 371 (Qwen3.5-9B) |
| tools list changing on tool_search activation | `stable_toolset` ON: activated tools stay in catalog | tools block byte-identical all session |
| reflection/ultrawork system injections | same demote path | no front re-eval on checkpoints |

## Warm cache across sessions (`slot_cache`, auto on local hosts)

**What.** llama.cpp `POST /slots/0?action=save|restore` persists the KV
state to disk on TUI exit and restores it before the first request of a
resumed session, so resume does not pay a cold prefill of the whole
history. `internal/llm/slotcache.go`.

**Gating (twice).** Construction: never even built for cloud/public base
URLs (they don't implement /slots and must not be probed). First use is
the probe: any failure (HTTP 501 without `--slot-save-path`, 404, network)
permanently disables it for the process — zero retries, zero noise.

**Measured (2026-07-05).** Dense model (Ministral-3-3B): resume eval
1712 → 88 (−95%, cache hit on the order of the whole history). **Hybrid
models (Qwen3.5 family): a silent no-op** — llama.cpp's slot files store
no context checkpoints, and recurrent/linear layers cannot roll back to a
divergence point without one, so restore degrades to a clean full
re-eval. Safe, just not profitable until llama.cpp persists checkpoints.
This is an upstream limitation, documented in slotcache.go.

## Preflight repo context (`preflight_repo`, **default ON**)

**What.** A compact auto-collected repo-state block (branch/HEAD,
uncommitted changes, recent commits — or a pure-Go recently-modified-files
fallback when git is absent; git is never required) appended to the first
user message of a session and to every worker briefing. Hard token budget
(800, most-important-first trimming). `internal/system/preflight/`.

**Why ON.** Measured 2026-07-09, identical task with/without: the ~73
token block turned 6 turns into 4 (−33%), eval −36%, total tokens −42%,
same end result — it deletes the "where am I" discovery turns. It rides
the *variable* side of the prompt (user message), never the system
prefix, so the cacheable front is untouched (asserted by
loop_preflight_test.go; live cache share stayed comparable).

## Noop-gate (`noop_gate`, **default OFF — deliberately**)

**What.** For batch (`--batch`) runs only: if the working-tree
fingerprint (path+size+mtime manifest, `internal/system/manifest`) is
identical to the one saved after the last successful run of the *same
prompt*, skip the run entirely — zero LLM calls, exit 0, a `no-op:` line.
Strictly fail-open: any doubt (missing manifest, IO error, changed tree)
means "run normally". Interactive sessions are never gated.

**Measured (2026-07-09).** Repeat identical batch run: 0 server requests
(llama-server log did not grow). After touching a file: gate opens,
normal run.

**Why OFF by default.** The gate changes *answer semantics* for
question-shaped prompts: a repeated identical batch QUESTION would return
"no-op" instead of the answer. That violates least surprise, so it is an
opt-in for idempotent pipeline-style batch jobs (defaults_test.go pins
the reasoning).

## Turn economy as a design principle

A round-trip on a slow local model costs seconds-to-minutes of wall
clock; tokens are cheap by comparison. Hence:

- preflight: pay ~73 tokens once, save 2 discovery turns (above);
- thin protocol keeps *first-call accuracy* a hard requirement — a
  slimmer schema that causes a failed call + retry is a net loss (every
  schema cut was live-tested for 1-call success, e.g. ctx_execute 5/5);
- soft-budget prompt discipline (commit 2feba19): the model is told that
  proportional edits and "no change needed" are valid answers, so it
  does not burn turns on cosmetic edits;
- `task` results return as one report instead of tool-call traffic in
  the main chat;
- draft-verify's sieve rejects bad drafts *before* the big model spends
  a turn on them.

When adding a feature, price it in turns first, tokens second, and put
the measurement in a telemetry line so the trade stays visible.
