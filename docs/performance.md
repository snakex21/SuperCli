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

## Phase telemetry (per-step breakdown, **default ON**)

**What.** Every agent step is timed phase-by-phase and counted, so
turn-economy work gets measured, not guessed (`internal/system/stats/`,
fed by the loop since df8fbca). Per step it records:

- **Phases** — wall time per canonical phase: `context_prepare`,
  `request_encode`, `backend_wait` (= TTFT), `stream_total`,
  `tool_execution`, `session_persist`, `next_turn_prepare`
  (remainder), plus a `tool:<name>` entry per executed tool;
- **ToolCalls** — raw tool-call batch size per step (duplicates
  count) — THE metric for judging read-only tool parallelism;
- tokens in/out per turn (same numbers as the `[tokens]` line).

All timings are whole-phase `time.Since` measurements; TTFT is a single
timestamp at the first delta, so the streaming hot path stays
allocation-free.

**How to look at it.**

- TUI: `/cost` — per-turn table with a calls column, plus a "Phase
  breakdown" section (per-step lines, phase totals, tool-calls
  distribution: avg/step and steps with >1 call);
- batch: one greppable stderr line per step —
  `[phase] step=N calls=N in=N out=N <phase>=<ms> ...`;
- programmatic: `stats.Save(path, turns, calls)` dumps the snapshot as JSON.

**Why ON.** No new knobs — the recorder was already wired by default;
the loop now feeds it every step. Cost is a handful of timestamps per
step, invisible next to a local-model turn.

### Purpose-labeled model calls (default ON)

**What.** Every `Provider.Complete` in the process — not just the main
step call — is metered centrally by the `llm.Metered` decorator on the
provider (installed by `internal/llm/factory`; call sites re-label via
`llm.WithPurpose` / `llm.WithBackground` on the context). Per call it
records: purpose, model + provider, TTFT, total time, tokens in/out,
foreground/background, canceled/failed. Purposes: `main`, `navigator`,
`compact`, `reflection`, `draft`, `verdict`, `memory` (autosave +
startup raw-log summarization, background), `goal`, `consult`,
`judge`, `darwin_judge`, `task` (task_model workers), `title` (webgui).

**Why.** Helper inferences used to be invisible or booked to the wrong
phase — the audit found a step with `next_turn_prepare=14s` of which
13.9s was a hidden model call. Model-powered aux operations inside a
step (draft, auto-compact summary, reflection) are now booked as their
own `model:<purpose>` phases and subtracted from `context_prepare` /
the `next_turn_prepare` remainder, so those phases measure PURE CLI
overhead.

**How to look at it.**

- TUI: `/cost` — "Model calls" section: per purpose count, total time,
  average TTFT, tokens, background/canceled/failed markers;
- batch: one greppable stderr line —
  `[calls] <purpose>=<n>x/<time>/in=<tok>/out=<tok> ...`;
- programmatic: `stats.Save(path, turns, calls)` includes the ledger,
  `stats.SumCalls` aggregates it.

### Foreground beats background: idle memory autosave (default ON)

**What.** Background model calls no longer compete with the user's
turn. The per-turn incremental memory summary used to fire an extra
inference IMMEDIATELY after every answer — racing the user's next
question for the same local backend (worse TTFT, KV-prefix churn).
Now:

- **deterministic user facts** (pure string matching, no model) are
  still saved immediately after each turn — a "nazywam się Maks"
  survives even a kill seconds later;
- the **model-backed summary** waits until the user has been idle for
  15 s (a constant, not a knob). Several turns finishing inside one
  window are batched into ONE summary call;
- a **new prompt cancels** the in-flight background summary
  (context cancel) and stops the idle timer; the uncovered fragment
  is retried — batched with newer turns — at the next idle window;
- **startup raw-log summarization** waits for the same idle window,
  so it is never in the way of the user's first question;
- **exit makes no model call**: the un-summarized conversation tail
  is stored verbatim as a raw-log entry (same mechanism as the
  abrupt-close handler) and the NEXT startup summarizes it in its
  idle window. Exits are instant; nothing is lost, only saved later.

On top of that, `llm.Metered` gives foreground calls strict priority:

- at most ONE background inference (memory autosave, startup
  summarization, webgui title) runs process-wide;
- a new foreground call cancels background work that is streaming or
  waiting on the background gate;
- background work starting while one or more foreground streams are
  active waits until ALL of them finish; foreground calls never wait
  for one another;
- the registration and foreground counter share one lock, so a
  start-vs-preempt race can only resolve as "background registered and
  canceled" or "foreground registered and background waits".

The metering relay uses a fixed 32-delta buffer: small enough to keep
streaming bounded, large enough to avoid a goroutine hand-off for every
token-sized provider fragment. Synthetic 1000-delta benchmark on the
2026-07-12 Windows dev host: bare provider ~53 ns/delta, metered relay
~160 ns/delta (previous unbuffered relay ~283 ns/delta), about 10.7 ms
of decorator work even for an unusually fragmented 100k-delta stream.

**Why.** On a single local backend every concurrent request splits
compute and evicts the KV prefix of the main conversation. The main
conversation is the product; helper inferences are bookkeeping —
bookkeeping runs when the user is not looking.

**Batch mode** (`--batch`) is unaffected: it never ran memory
autosave and still doesn't — one prompt, pure stdout, exit.

### Signal-driven reflection (default ON)

Mid-run self-review used to launch an extra foreground model inference every
eight tool steps whether the run was healthy or not. On a slow local backend
that means another full prefill and it cannot be hidden behind CLI work.

The default `reflect_every = 0` is now adaptive and deterministic. Reflection
fires only after the model has already had one normal opportunity to recover
from a tool failure (two consecutive failing batches), repeats the exact same
tool names and arguments in consecutive steps, or reaches the last checkpoint
that can still influence a run before `MaxSteps`. Different arguments to the
same tool count as progress. The detector hashes batches, so large tool
arguments do not remain resident in loop state, and resets after every
reflection to prevent a call on every subsequent step.

An explicit positive `reflect_every = N` retains the historical fixed interval
for users who want it; a negative value disables reflection. Purpose telemetry
continues to report every actual call as `reflection`, so `/cost` shows the
saved inferences directly.

### Cross-backend read batching (default ON)

`read_many` fetches up to 12 independent file ranges in one tool call, with a
300-line cap per range and a 32 KB global result cap. Its compact
`file:from-to | file:from-to` argument works unchanged with native cloud tool
calling and the sentinel protocol used by small local models. Individual read
failures are returned beside successful ranges instead of discarding the whole
batch, so one missing file does not force another recovery turn.

Ranges are streamed from disk instead of loading whole files. Even a multi-MB
single line is consumed in fixed-size chunks, retained as a short UTF-8-safe
prefix with an explicit truncation marker, and never multiplied into 12 full
file buffers when the reads run concurrently.

The tool itself reads ranges concurrently and renders them in request order.
Separately, when a native model emits several calls in one response, the loop
runs them concurrently only if every registered tool explicitly declares
`ReadOnly`. Any unknown or mutating tool keeps the entire batch sequential.
This parallelises local I/O, never model inference: a single-GPU backend still
uses the existing sequential worker policy, while cloud backends retain their
safe inference parallelism.

The core prompt spends one short sentence asking models to batch independent
file ranges. Replay coverage pins the important economy contract: three file
ranges, one tool call, one follow-up model turn.

### Direct simple tools without a search turn (default ON)

`invoke_tool` is one schema-stable dispatcher shared by native cloud tool
calling and the sentinel protocol. It advertises at most 16 registered tools
that are both certified `ReadOnly` and described by a flat scalar schema.
Native calls pass an `args` object; sentinel calls use `arg.<field>` lines. Some
local XML chat templates stringify nested objects, so the dispatcher also
accepts a JSON-object string or flat `key: value` text and then applies the same
target-schema validation. The loop rewrites a valid dispatch to the real target
before history, verification, error attribution and telemetry, so no subsystem
sees a fake wrapper tool.

Unknown arguments, nested arrays/objects/unions and every mutating tool are
rejected with `requires tool_search`. Replay pins the turn win: direct call →
result → answer in two provider turns, with no intermediate `tool_search`
round-trip. The small-model catalog separates directly callable read-only
entries from complex load-on-demand entries; cloud models see the same compact
eligible signatures in the dispatcher's description.

### One execution profile across TUI, WebGUI and batch

All three front-ends resolve the same two independent axes before creating a
loop: model capability (tier rules, price and model metadata) and backend shape
(local/private versus cloud). Small-capability models receive the short core
prompt and compact tool protocol. A large model keeps the richer guidance, but
on a local/private host its tool schemas are still thinned because prefill, not
reasoning quality, is the bottleneck. The profile is frozen for the loop so the
tool prefix never changes mid-session. `small_full_tools = true` remains the
explicit full-schema escape hatch.

`SUPERCLI_LLM_MAX_TOKENS` is carried through OpenAI-compatible and OpenCode
transports as `max_tokens` (zero omits it). Batch streaming writes delta
fragments verbatim rather than adding a newline after every fragment.
Repository preflight waits for a coordinator/project turn, so a greeting or
general-advice turn no longer pays for repository state.

### Deterministic advisor routing

Navigator auto mode treats strong conceptual prefixes (`wyjaśnij`, `jak
działa`, `what is`, `explain`, etc.) as a confident advisor route. Project/file
keywords are checked first, so “wyjaśnij ten kod w pliku” remains coordinator.
With `navigator = auto`, ambiguity safely falls back to coordinator without a
model call. If a separate task/draft provider is configured, the TUI may use it
for ambiguous classification without evicting the main model's KV cache.
`navigator = on` remains the explicit model-every-turn mode.

### Catalog-hoist live A/B harness

`TestCatalogHoist_AB_Live` runs three real turns per arm against
`SUPERCLI_LIVE_BASEURL` / `SUPERCLI_LIVE_MODEL`, discards each cold first call,
and compares provider-reported evaluated input (`input - cached`) for tail vs
hoisted catalog placement. A second guard runs with normal thinking and
requires the model to discover and execute a direct `catalog_probe` in both
placements without tool errors. It logs repeats and terminal loop errors but
does not compare them for Qwen builds with a known stochastic tool-loop issue.

2026-07-12 live result: LM Studio, Qwen3.5-9B Q8, full GPU offload, 32k context,
parallelism 1. Tail evaluated 2891 warm input tokens; hoist evaluated 2867, a
24-token / 0.83% difference. Both quality arms completed in one tool call.
LM Studio's OpenAI-compatible usage reported zero cached tokens for every arm,
so this run could not establish a KV-cache win; the small input difference was
only prompt shape. The live run also exposed an XML-template compatibility issue where Qwen
encoded nested `invoke_tool.args` as text; the tolerant, schema-checked decoder
now prevents the otherwise necessary repair turns.

2026-07-13 live result: llama.cpp on HP Z6, Qwen3.5-122B-A10B Q4_K_P. The two
warm tail turns evaluated 1743 input tokens (725 cached per turn); the hoisted
turns evaluated 237 (1466 cached per turn): **1506 fewer evaluated tokens,
86.4%**. With normal thinking enabled, both placement quality arms discovered
and executed `catalog_probe`. Tool-call counts are logged but are not used as a
placement verdict because this Qwen build has a known stochastic looping
tendency; one observed run produced five calls at the tail and one when
hoisted, but that comparison is informational rather than causal. The run also proved that strict Qwen templates
require one leading system message, so the base system prompt and hoisted
catalog are merged into one stable block. Catalog hoist is now automatic for
thin+stable profiles; `stable_toolset=false` or `small_full_tools=true` is the
escape hatch.

### Bounded large-result store

Successful tool output up to 8 KiB remains byte-identical. Larger output is
kept in a per-loop in-memory LRU (32 entries / 16 MiB) while provider history
receives a roughly 4 KiB head/tail preview and a `read_output` handle. The UI
event retains the complete result. This reduces local-model prefill without
blindly discarding evidence: the model can fetch another 8 KiB range only when
needed. Handles intentionally expire with the run and are never written to
disk.

## Structured tool errors (deterministic failure results)

**What.** When a tool fails, the model gets a short, deterministic,
machine-shaped reason instead of a raw Go/OS error — so a small model
fixes itself in one turn instead of guessing at "exit status 1".
The CLI states only facts it is certain of (exit code, timeout,
truncation, path); it never guesses causes.

**Process tools** (`ctx_execute` via `ctxexec.FailureSummary`, user
tools via `commandFailedErr`): first line is the fact line, then the
tail of the captured streams — errors live at the end, so tails keep
the LAST bytes, capped (2 KB per stream) with an explicit marker:

```
command_failed exit=1 (1.3s)
stderr:
FAIL: TestFoo ...
```

- timeout: `command_failed timeout exit=124 (10.0s)` + partial output;
- process never started: `command_failed: exec: "nope": executable
  file not found ...` (raw exec reason, verbatim);
- over-long streams: `stderr (tail, truncated):` marker, same
  retry-safe convention as the c74e100 read-tool caps. Before this,
  a failed `ctx_execute` surfaced only `ctx_execute: exit 1` — the
  stderr the model needed was dropped on the error path.

**File tools** (`fileops.FileErr`, used by read_lines/read_context/
edit_line(+anchored)/insert_after/delete_lines/write_file/move/copy/
trash/list_dir): stable keyword + the exact path that was tried,
instead of OS prose like `open C:\...: The system cannot find the file
specified.`:

```
not_found C:\proj\data.txt
permission C:\proj\locked.txt
is_directory C:\proj\src
```

Unknown causes pass through unchanged (no fact, no rewrite). The error
attribution heuristics (F4.d) recognise the structured forms, so error
-log classification keeps working. Success outputs are untouched —
only the failure path changed.

The office/media readers (`read_pdf`, `read_docx`, `read_xlsx`,
`read_zip`, `read_image`, `edit_docx`, `edit_xlsx`) route their
stat/open/read failures through the same `fileops.FileErr` forms.

**Web tools** (`web_fetch`, `web_search` — all engines): a non-200
response keeps the error body instead of dropping it (API errors
carry the fix, e.g. `{"code":"missing_api_key",...}`):

```
http_failed status=401 host=api.search.brave.com content_type=application/json retry_after=30
body:
<first 2 KB + last 2 KB, UTF-8-safe, omitted_bytes marker>
```

Transport failures (no response at all) get a deterministic cause
token the model can branch on: `request_failed cause=timeout|dns|
tls|canceled|error host=<h>: <err>`. Request headers (Authorization,
tokens) are never echoed.

**search_code**: a real ripgrep failure (bad pattern, crash — NOT
exit 1, which means "no matches" and stays a valid result) falls
back to the Go scanner; if that also fails the model gets a
structured `search_failed ...` error instead of a fake result text.
The `max` cap is now truly global: rg output is read streaming and
the process is killed once the limit is hit (rg's `--max-count` is
per file).

**Output caps at the boundary** (all head+tail with an explicit
`[... omitted_bytes=N ...]` marker, UTF-8-safe cuts,
`core.HeadTail` / `core.HeadTailBuffer`):

- user tools (shell/script): combined output bounded DURING the run
  (first 8 KB + last 8 KB) — replaces `CombinedOutput()`, which held
  arbitrary output in RAM before truncating;
- MCP tools: external server results capped at 16 KB + 4 KB on
  success, standard 2 KB error cap on `IsError` — a chatty server
  can no longer inject megabytes into one tool result.

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
user message of a session and to every worker briefing. Small worktrees keep
exact paths; large dirty trees use status counts, hot areas and a 16-path
sample. Hard token budget: 300 (most-important-first trimming).
`internal/system/preflight/`.

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

## Worker retention and concurrency (defaults, no toml knobs)

Every worker holds its whole Loop (full conversation history), so an
unbounded registry is a slow memory leak in long coordinator sessions.
Two process-wide constants (`internal/agent/worker_registry.go`),
env-overridable, no new config fields:

- **Retention of finished workers: 20** (`SUPERCLI_WORKER_RETENTION`).
  The oldest finished (done/failed/stopped) workers beyond the cap are
  evicted LRU by UpdatedAt; a compact summary (status, error, tokens,
  `core.HeadTail`-capped last result) is kept, so `/workers`,
  `send_message` and `task_stop` answer "evicted, here is what it did"
  instead of "unknown worker". Active workers are never evicted.
- **Max concurrent active workers: 6** (`SUPERCLI_MAX_ACTIVE_WORKERS`).
  There is no read/write worker classification in the codebase, so this
  is one global cap; an over-limit `task` fails fast with guidance
  (wait / task_stop / send_message) rather than queueing inside a tool
  call and stalling the coordinator's turn.

## Allocation benchmark baseline (2026-07-12)

`go test -tags benchmark -run xxx -bench . -benchmem ./test/`
(test/benchmark_alloc_test.go + test/benchmark_test.go). Compare with
benchstat after touching providerMessages/consume/prune, tool
dispatch, `core.HeadTailBuffer`, or the worker registry. Reference
box: Ryzen 7 5800X3D, go1.26.2 windows/amd64.

```
BenchmarkLongSessionPrepare/msgs=100-16              46556      26673 ns/op    92216 B/op       28 allocs/op
BenchmarkLongSessionPrepare/msgs=500-16              10000     100832 ns/op   317787 B/op       29 allocs/op
BenchmarkLongSessionPrepare/msgs=2000-16              2599     463617 ns/op  1179129 B/op       31 allocs/op
BenchmarkConsumeLargeStream/deltas=1000-16            4036     287740 ns/op    94290 B/op     1039 allocs/op
BenchmarkConsumeLargeStream/deltas=10000-16            416    2864172 ns/op   707412 B/op    10050 allocs/op
BenchmarkConsumeLargeStream/deltas=100000-16            40   28532732 ns/op  6876053 B/op   100062 allocs/op
BenchmarkToolBatch/calls=4-16                        25069      50654 ns/op    76980 B/op      241 allocs/op
BenchmarkToolBatch/calls=16-16                       10000     123602 ns/op   128469 B/op      826 allocs/op
BenchmarkToolBatch/calls=64-16                        2678     438534 ns/op   322835 B/op     3139 allocs/op
BenchmarkContextReport/msgs=200-16                    6332     277399 ns/op   143547 B/op     1490 allocs/op
BenchmarkContextReport/msgs=1000-16                    796    1480923 ns/op   693543 B/op     8040 allocs/op
BenchmarkHeadTailBuffer/total=1MB-16                 23900      51280 ns/op    51388 B/op        8 allocs/op
BenchmarkHeadTailBuffer/total=16MB-16                 8205     151960 ns/op    51380 B/op        8 allocs/op
BenchmarkHeadTailBuffer/total=64MB-16                 2131     569407 ns/op    51382 B/op        8 allocs/op
BenchmarkWorkerRegistry/add_sweep/finished=100-16    31711      38074 ns/op     2760 B/op       14 allocs/op
BenchmarkWorkerRegistry/counts/finished=100-16     1000000       1057 ns/op        0 B/op        0 allocs/op
BenchmarkWorkerRegistry/list/finished=100-16        106268      11140 ns/op      952 B/op        3 allocs/op
BenchmarkWorkerRegistry/add_sweep/finished=1000-16    2162     515434 ns/op    18130 B/op       17 allocs/op
BenchmarkWorkerRegistry/counts/finished=1000-16      90990      13387 ns/op        0 B/op        0 allocs/op
BenchmarkWorkerRegistry/list/finished=1000-16         6475     181606 ns/op     8248 B/op        3 allocs/op
BenchmarkTokenThroughput-16                           2773    6188995 ns/op  1022891 B/op     1287 allocs/op
BenchmarkToolDispatch-16                          48786834      24.73 ns/op        2 B/op        1 allocs/op
BenchmarkEvictForBudget_1kMsgs-16                     4263     288340 ns/op   230792 B/op        5 allocs/op
BenchmarkEvictForBudget_10kMsgs-16                     486    2483012 ns/op  2256277 B/op        7 allocs/op
```

Reading the baseline:

- **ConsumeLargeStream is the 593a352 regression pin**: ns/op scales
  ~10× per 10× deltas (288 µs → 2.86 ms → 28.5 ms) — linear, and
  allocs ≈ 1/delta (the MessageEvent). A superlinear jump between
  sizes means the quadratic `text += delta` came back.
- **HeadTailBuffer is size-independent in memory**: 8 allocs and
  ~51 KB regardless of 1 MB or 64 MB streamed — the bound-during-run
  guarantee.
- **LongSessionPrepare** grows linearly with history (allocs stay
  ~flat — the cost is the visible-view copy, not churn).
- **WorkerRegistry add_sweep** is the retention sweep (status scan +
  LRU sort) on a registry FULL of finished workers; ~0.5 ms at 1000 is
  fine because retention caps the real registry at 20 (29194ae) — the
  benchmark exists to catch accidental O(n²) in the sweep.
- **EvictForBudget is the single-pass regression pin** (2026-07-12):
  mass evict of nearly the whole history scales linearly (0.29 ms at
  1k msgs → 2.5 ms at 10k). The pre-rewrite loop re-ran
  `EstimateVisibleTokens()` per eviction — O(n²): 75.1 ms at 1k and
  6.93 s (!) at 10k on the same box (~260× / ~2800× slower). A
  superlinear jump between the two sizes means the per-iteration
  recount came back.

## Recent fixes and experiments (2026-07-12)

- **Hidden messages persist across Runs** (9731077): Run() no longer
  resets the hidden map, so /clear, hide_messages and budget
  evictions issued between Runs actually stay out of the next
  provider request (and the KV-cache prefix stays stable). Hides are
  reset only when their indices die: compaction and /resume.
- **EvictForBudget single pass**: the eviction loop re-ran the full
  visible-token estimate per evicted message — O(n²) on a mass
  evict. Now: price once, subtract per message, one exact final
  check. 1k msgs 75.1 ms → 0.29 ms, 10k msgs 6.93 s → 2.5 ms
  (BenchmarkEvictForBudget baseline above).

## Web GUI request hot path (2026-07-13)

`Engine` owns one lazy, concurrency-safe `session.Store`; endpoints, chat
history, per-call usage, titles, stats and checkpoint events reuse it. This
removes repeated SQLite `Ping` plus schema/FTS migration checks. Baseline on
the same Ryzen 7 5800X3D Windows host:

```
go test ./internal/webgui -run '^$' -bench BenchmarkEngineSessionStore -benchmem -benchtime=200ms
BenchmarkEngineSessionStore/shared_handle-16       5.7-5.9 ns/op       0 B/op      0 allocs/op
BenchmarkEngineSessionStore/open_and_migrate-16    1.28-1.32 ms/op    ~14.9 KB/op  514-515 allocs/op
```

Assistant text SSE events are coalesced for at most 40 ms or 4 KB. Tool,
worker, question, notice, done and error boundaries flush pending text first
and remain immediate. The browser already batches Markdown rendering; this
server-side layer removes redundant JSON encodes, flush syscalls and WebView
event dispatches.

## Bounded durable memory (2026-07-13)

Persistent memory is an indexed working set, never a transcript file injected
wholesale into the model. The session-start briefing remains hard-capped at
700 estimated tokens (300 on the small tier); older entries are available only
through the explicit `recall` tool. Disk and tool-output safety rails prevent
the multi-gigabyte memory-file failure mode seen in some other CLIs:

- 16 KiB maximum for any stored entry; `remember` asks the model to summarize
  at 4 KiB instead of accepting transcript dumps;
- 4096 live entries and 32 MiB source text per project/global store;
- rolling retention of 200 automatic task logs, 3 emergency raw tails and 200
  scratch notes per day; durable preferences/decisions are not silently pruned;
- `recall` clamps requests to 10 hits, 1800 bytes per hit and about 8 KiB total,
  always marking truncation;
- SQLite FTS/vector indexes stay on disk and only the selected bounded result
  reaches the provider, so database size does not translate into prompt cost.

The web prompt replay test also found that `task.agent.enum` inherited Go map
iteration order from `SubAgentRegistry.Names`. Names are now sorted, keeping
both the system prompt and model-facing tool catalog byte-stable across turns
for KV-cache reuse.

## Recent fixes and experiments (2026-07-11)

- **EvictForBudget threshold fix** (b2a393c): eviction now compares
  against the session cap instead of tokens already used, so history
  is no longer evicted too early.
- **Streaming consume() O(n)** (593a352): the incremental marker
  scanner replaces the quadratic `text += delta` accumulation on long
  streamed answers.
- **Catalog hoist**: moves the thin-tools catalog into the stable prompt
  prefix. Default ON for thin+stable profiles after HP Z6/Qwen3.5-122B
  measured 86.4% fewer evaluated warm-turn tokens. Thinking-enabled probes
  confirmed tool visibility in both placements; repeat counts are excluded
  because this Qwen build can loop stochastically. Disable through
  `stable_toolset=false` or `small_full_tools=true`.
- **Navigator on the small provider** (fadc051): route classification
  for the navigator runs on the small side provider; awaiting live
  test.

## Local tool-workload live benchmark (2026-07-14)

`TestToolWorkload_Live` is an opt-in, repeatable end-to-end workload for any
OpenAI-compatible local or cloud backend. Every trial creates a fresh tiny Go
project with one deterministic production bug. The model must use
`read_many -> edit_line -> ctx_execute(go test ./...)`; the harness then runs
an independent `go test` and inspects the production file. Prompt/model text is
not persisted. The measurement stops at the first green `ctx_execute`, so a
local model's optional final prose cannot hide time-to-verified-change.

```powershell
$env:SUPERCLI_EVAL_TOOL_URL='http://host:port/v1'
$env:SUPERCLI_EVAL_TOOL_MODEL='model-id'
$env:SUPERCLI_EVAL_TOOL_TRIALS='10'
go test ./internal/agent -run '^TestToolWorkload_Live$' -count=1 -v -timeout 35m
```

HP Z6 / Qwen3.5-122B-A10B Q4_K_P, thinking enabled, 10 sequential trials:

- success: **10/10**; zero tool failures;
- average time to independently verified fix: **137.954 s**;
- average model calls: **3.9**; tool calls: exactly **3.0**;
- aggregate model wait + streaming: **1371.938 s** (about 99.45%);
- aggregate tools, including ten real `go test` runs: **7.592 s**;
- aggregate CLI context preparation: **6 ms**.

The exploratory baseline also exposed three high-leverage local-model
compatibility gaps before the green run: redundant quotes around an empty
`list_dir` path, omitted leading indentation in an otherwise exact
`edit_line` anchor, and bare filenames in `read_many`. Safe normalization for
those cases plus Windows `USERPROFILE`/`LOCALAPPDATA`/Go-cache preservation in
the scrubbed command environment removed the repair turns. A deliberately
underspecified discovery prompt remains a separate harder workload; it failed
0/2 initial probes and must not be conflated with the scoped 10/10 result.

## Durable Goal live benchmark (2026-07-14)

`TestGoalWorkload_Live` exercises the complete persistent Goal contract on a
real backend. Each isolated trial creates an active SQLite goal with one task
and a fresh buggy Go project. The model must inspect, edit and run `go test`,
then perform `complete_task -> verify -> mark_done`. The harness stops at the
durable `mark_done`, reopens SQLite, checks the terminal goal/task/verification
state, and independently runs the test again. A red diagnostic test is tracked
separately from a tool/protocol failure.

```powershell
$env:SUPERCLI_EVAL_GOAL_URL='http://host:port/v1'
$env:SUPERCLI_EVAL_GOAL_MODEL='model-id'
$env:SUPERCLI_EVAL_GOAL_TRIALS='3'
go test ./internal/agent -run '^TestGoalWorkload_Live$' -count=1 -v -timeout 30m
```

HP Z6 / Qwen3.5-122B-A10B Q4_K_P, thinking enabled, three sequential final
trials after the compatibility fixes:

- success: **3/3**, zero tool and protocol failures;
- average time to independently verified, durably closed goal: **273.925 s**;
- average model calls: **6.33**; average tool calls: **6.33**;
- aggregate backend wait + streaming: **819.421 s** (about 99.71%);
- aggregate tools, including three real `go test` runs: **2.344 s**;
- aggregate CLI context preparation: **6 ms**;
- two trials used the minimal six-call sequence; one emitted a duplicate
  `verify` in the same model turn, which did not add an inference or corrupt
  state.

The discovery runs were deliberately retained as engineering evidence. They
exposed local-model ambiguities that ordinary unit tests did not: Goal action
names emitted as standalone tools, logical ids (`current_goal`), schema
placeholders (`<id>`), the active title copied into `goal_id`, verification
evidence sent as `result`/`evidence`, and a Goal call wrapped in `invoke_tool`.
The fixes are bounded aliases around the same validated Goal service; they do
not bypass open-task, evidence, or persistence checks. An active WebGUI goal
also exposes the Goal schema immediately (ordinary no-goal chat still keeps it
dormant), removing repeated discovery inferences while retaining a stable
KV-cache prefix. Finally, after any failed concrete tool result the loop blocks
`complete_task` and passing verification until a later concrete action
succeeds, so a red test cannot be immediately declared complete.
