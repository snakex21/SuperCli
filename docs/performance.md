# Performance â€” telemetry, caches, turn economy

Design rule behind all of it: **on a slow-prefill server the costs are, in
order: a turn > a prompt-front re-eval > appended tokens.** Optimize in
that order, and never guess â€” every mechanism here shipped with a live
measurement.

## Telemetry (how the numbers are obtained)

**What.** Every turn ends with a token line: `[tokens] in=... out=...
total=... cache=... eval=...` in batch stderr (app/main.go), the same
data on the TUI/web done-event (`cache | eval | gen`, cache-hit %).

**How.** `cache` is the provider-reported cached prompt tokens
(`usage.prompt_tokens_details.cached_tokens`, which llama.cpp mirrors
from its `timings.cache_n`); `eval = in â’ cache` is what the server
actually prefilled. Verified token-exact against server logs during the
2026-07-05 cache hunt. This line is the ground truth used for every
number below â€” if you change prompt construction, watch it.

## Phase telemetry (per-step breakdown, **default ON**)

**What.** Every agent step is timed phase-by-phase and counted, so
turn-economy work gets measured, not guessed (`internal/system/stats/`,
fed by the loop since df8fbca). Per step it records:

- **Phases** â€” wall time per canonical phase: `context_prepare`,
  `request_encode`, `backend_wait` (= TTFT), `stream_total`,
  `tool_execution`, `session_persist`, `next_turn_prepare`
  (remainder), plus a `tool:<name>` entry per executed tool;
- **ToolCalls** â€” raw tool-call batch size per step (duplicates
  count) â€” THE metric for judging read-only tool parallelism;
- tokens in/out per turn (same numbers as the `[tokens]` line).

All timings are whole-phase `time.Since` measurements; TTFT is a single
timestamp at the first delta, so the streaming hot path stays
allocation-free.

**How to look at it.**

- TUI: `/cost` â€” per-turn table with a calls column, plus a "Phase
  breakdown" section (per-step lines, phase totals, tool-calls
  distribution: avg/step and steps with >1 call);
- batch: one greppable stderr line per step â€”
  `[phase] step=N calls=N in=N out=N <phase>=<ms> ...`;
- programmatic: `stats.Save(path, turns, calls)` dumps the snapshot as JSON.

**Why ON.** No new knobs â€” the recorder was already wired by default;
the loop now feeds it every step. Cost is a handful of timestamps per
step, invisible next to a local-model turn.

### Purpose-labeled model calls (default ON)

**What.** Every `Provider.Complete` in the process â€” not just the main
step call â€” is metered centrally by the `llm.Metered` decorator on the
provider (installed by `internal/llm/factory`; call sites re-label via
`llm.WithPurpose` / `llm.WithBackground` on the context). Per call it
records: purpose, model + provider, TTFT, total time, tokens in/out,
cached/evaluated prefill tokens, estimated prefill tok/s, active prompt budget,
foreground/background, canceled/failed. Purposes: `main`, `navigator`,
`compact`, `reflection`, `draft`, `verdict`, `memory` (autosave +
startup raw-log summarization, background), `goal`, `consult`,
`judge`, `darwin_judge`, `task` (task_model workers), `title` (webgui).

**Why.** Helper inferences used to be invisible or booked to the wrong
phase â€” the audit found a step with `next_turn_prepare=14s` of which
13.9s was a hidden model call. Model-powered aux operations inside a
step (draft, auto-compact summary, reflection) are now booked as their
own `model:<purpose>` phases and subtracted from `context_prepare` /
the `next_turn_prepare` remainder, so those phases measure PURE CLI
overhead.

**How to look at it.**

- TUI: `/cost` â€” "Model calls" section: per purpose count, total time,
  average TTFT, tokens, background/canceled/failed markers;
- batch: one greppable stderr line â€”
  `[calls] <purpose>=<n>x/<time>/in=<tok>/out=<tok> ...`;
- programmatic: `stats.Save(path, turns, calls)` includes the ledger,
  `stats.SumCalls` aggregates it.

The WebGUI's durable `session_usage` rows persist the same TTFT, evaluated
tokens, tok/s and budget fields. `/context` shows the latest sample and learned
budget. No prompt text, endpoint URL, header or credential is stored.

### Adaptive prefill budget (default ON)

The hard model window answers “will this request fit?”, not “will this request
prefill quickly?”. SuperCli therefore learns a separate target-sized prompt
per configured connection and model from actual TTFT and cache reuse. The
target is 15 seconds; a 30-second sample activates protection immediately,
while less severe slowdowns need repeated evidence. Prune and compaction use
the learned threshold only when it is lower than the hard safety threshold.
Fast samples near the edge raise it gradually.

The decision is deliberately transport-neutral. A model added by the user over
HTTP is neither assumed local nor remote; only its measured behavior matters.
The portable profile lives at `supercli-data/prefill-profiles.json`.

### Foreground beats background: idle memory autosave (default ON)

**What.** Background model calls no longer compete with the user's
turn. The per-turn incremental memory summary used to fire an extra
inference IMMEDIATELY after every answer â€” racing the user's next
question for the same local backend (worse TTFT, KV-prefix churn).
Now:

- **deterministic user facts** (pure string matching, no model) are
  still saved immediately after each turn â€” a "nazywam siÄ™ Maks"
  survives even a kill seconds later;
- the **model-backed summary** waits until the user has been idle for
  15 s (a constant, not a knob). Several turns finishing inside one
  window are batched into ONE summary call;
- a **new prompt cancels** the in-flight background summary
  (context cancel) and stops the idle timer; the uncovered fragment
  is retried â€” batched with newer turns â€” at the next idle window;
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
conversation is the product; helper inferences are bookkeeping â€”
bookkeeping runs when the user is not looking.

**Batch mode** (`--batch`) is unaffected: it never ran memory
autosave and still doesn't â€” one prompt, pure stdout, exit.

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
rejected with `requires tool_search`. Replay pins the turn win: direct call â†’
result â†’ answer in two provider turns, with no intermediate `tool_search`
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

Navigator auto mode treats strong conceptual prefixes (`wyjaĹ›nij`, `jak
dziaĹ‚a`, `what is`, `explain`, etc.) as a confident advisor route. Project/file
keywords are checked first, so â€śwyjaĹ›nij ten kod w plikuâ€ť remains coordinator.
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
machine-shaped reason instead of a raw Go/OS error â€” so a small model
fixes itself in one turn instead of guessing at "exit status 1".
The CLI states only facts it is certain of (exit code, timeout,
truncation, path); it never guesses causes.

**Process tools** (`ctx_execute` via `ctxexec.FailureSummary`, user
tools via `commandFailedErr`): first line is the fact line, then the
tail of the captured streams â€” errors live at the end, so tails keep
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
  a failed `ctx_execute` surfaced only `ctx_execute: exit 1` â€” the
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
-log classification keeps working. Success outputs are untouched â€”
only the failure path changed.

The office/media readers (`read_pdf`, `read_docx`, `read_xlsx`,
`read_zip`, `read_image`, `edit_docx`, `edit_xlsx`) route their
stat/open/read failures through the same `fileops.FileErr` forms.

**Web tools** (`web_fetch`, `web_search` â€” all engines): a non-200
response keeps the error body instead of dropping it (API errors
carry the fix, e.g. `{"code":"missing_api_key",...}`):

```
http_failed status=401 host=api.search.brave.com content_type=application/json retry_after=30
body:
<first 2 KB + last 2 KB, UTF-8-safe, line-boundary cuts, omitted_bytes/omitted_lines marker>
```

Transport failures (no response at all) get a deterministic cause
token the model can branch on: `request_failed cause=timeout|dns|
tls|canceled|error host=<h>: <err>`. Request headers (Authorization,
tokens) are never echoed.

**search_code**: a real ripgrep failure (bad pattern, crash â€” NOT
exit 1, which means "no matches" and stays a valid result) falls
back to the Go scanner; if that also fails the model gets a
structured `search_failed ...` error instead of a fake result text.
The `max` cap is now truly global: rg output is read streaming and
the process is killed once the limit is hit (rg's `--max-count` is
per file).

**Output caps at the boundary** (all head+tail with an explicit
`[... omitted_bytes=N, omitted_lines=M ...]` marker, UTF-8-safe and line-boundary cuts,
`core.HeadTail` / `core.HeadTailBuffer`):

- user tools (shell/script): combined output bounded DURING the run
  (first 8 KB + last 8 KB) â€” replaces `CombinedOutput()`, which held
  arbitrary output in RAM before truncating;
- MCP tools: external server results capped at 16 KB + 4 KB on
  success, standard 2 KB error cap on `IsError` â€” a chatty server
  can no longer inject megabytes into one tool result.

## KV-cache discipline (summary; details in architecture.md)

The prompt must be byte-stable at the front, append-only at the tail.
The three historical cache-killers and their fixes, all live-measured:

| killer | fix | measured effect |
|---|---|---|
| minute-granular time stamp hoisted to prompt front | demote mid-conversation system msgs to tail (`system_demote.go`) | minute tick: eval 2216 â†’ 371 (Qwen3.5-9B) |
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
permanently disables it for the process â€” zero retries, zero noise.

**Measured (2026-07-05).** Dense model (Ministral-3-3B): resume eval
1712 â†’ 88 (â’95%, cache hit on the order of the whole history). **Hybrid
models (Qwen3.5 family): a silent no-op** â€” llama.cpp's slot files store
no context checkpoints, and recurrent/linear layers cannot roll back to a
divergence point without one, so restore degrades to a clean full
re-eval. Safe, just not profitable until llama.cpp persists checkpoints.
This is an upstream limitation, documented in slotcache.go.

## Preflight repo context (`preflight_repo`, **default ON**)

**What.** A compact auto-collected repo-state block (branch/HEAD,
uncommitted changes, recent commits â€” or a pure-Go recently-modified-files
fallback when git is absent; git is never required) appended to the first
user message of a session and to every worker briefing. Small worktrees keep
exact paths; large dirty trees use status counts, hot areas and a 16-path
sample. Hard token budget: 300 (most-important-first trimming).
`internal/system/preflight/`.

**Why ON.** Measured 2026-07-09, identical task with/without: the ~73
token block turned 6 turns into 4 (â’33%), eval â’36%, total tokens â’42%,
same end result â€” it deletes the "where am I" discovery turns. It rides
the *variable* side of the prompt (user message), never the system
prefix, so the cacheable front is untouched (asserted by
loop_preflight_test.go; live cache share stayed comparable).

## Noop-gate (`noop_gate`, **default OFF â€” deliberately**)

**What.** For batch (`--batch`) runs only: if the working-tree
fingerprint (path+size+mtime manifest, `internal/system/manifest`) is
identical to the one saved after the last successful run of the *same
prompt*, skip the run entirely â€” zero LLM calls, exit 0, a `no-op:` line.
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
- thin protocol keeps *first-call accuracy* a hard requirement â€” a
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
  ~10Ă— per 10Ă— deltas (288 Âµs â†’ 2.86 ms â†’ 28.5 ms) â€” linear, and
  allocs â‰ 1/delta (the MessageEvent). A superlinear jump between
  sizes means the quadratic `text += delta` came back.
- **HeadTailBuffer is size-independent in memory**: 8 allocs and
  ~51 KB regardless of 1 MB or 64 MB streamed â€” the bound-during-run
  guarantee.
- **LongSessionPrepare** grows linearly with history (allocs stay
  ~flat â€” the cost is the visible-view copy, not churn).
- **WorkerRegistry add_sweep** is the retention sweep (status scan +
  LRU sort) on a registry FULL of finished workers; ~0.5 ms at 1000 is
  fine because retention caps the real registry at 20 (29194ae) â€” the
  benchmark exists to catch accidental O(nÂ˛) in the sweep.
- **EvictForBudget is the single-pass regression pin** (2026-07-12):
  mass evict of nearly the whole history scales linearly (0.29 ms at
  1k msgs â†’ 2.5 ms at 10k). The pre-rewrite loop re-ran
  `EstimateVisibleTokens()` per eviction â€” O(nÂ˛): 75.1 ms at 1k and
  6.93 s (!) at 10k on the same box (~260Ă— / ~2800Ă— slower). A
  superlinear jump between the two sizes means the per-iteration
  recount came back.

## Recent fixes and experiments (2026-07-12)

- **Hidden messages persist across Runs** (9731077): Run() no longer
  resets the hidden map, so /clear, hide_messages and budget
  evictions issued between Runs actually stay out of the next
  provider request (and the KV-cache prefix stays stable). Hides are
  reset only when their indices die: compaction and /resume.
- **EvictForBudget single pass**: the eviction loop re-ran the full
  visible-token estimate per evicted message â€” O(nÂ˛) on a mass
  evict. Now: price once, subtract per message, one exact final
  check. 1k msgs 75.1 ms â†’ 0.29 ms, 10k msgs 6.93 s â†’ 2.5 ms
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

## Release performance smoke and model eval matrix (2026-07-14)

The release-only `cmd/supercli-perf` command measures a built binary over a
small repeated sample: process cold start, time to its first stdout/stderr
fragment, drain time after first output, and peak child-process RSS. It writes a
stable JSON report and can fail CI on a configured warning threshold. The
benchmark is a separate binary; ordinary SuperCli startup imports none of it.

```powershell
go build -o supercli.exe ./cmd/supercli
go run ./cmd/supercli-perf --binary .\supercli.exe --iterations 5 `
  --output test\perf\latest.json --fail-on-warning
```

The initial Windows development-host smoke measured three post-warmup samples:
cold-start p95 **11.43 ms**, first-output p95 **10.88 ms**, and peak RSS
**10.24 MB**. These are a local baseline, not universal release limits.

`cmd/supercli-eval` complements microbenchmarks with agent quality. Its bundled
suite contains ten isolated Go tasks and scores expected/forbidden file changes,
verification argv, optional JSONL trace events, timeout, and model matrix runs.
Validation is fully offline; live calls happen only through the explicitly
supplied agent command.

## Repeated observations

Loop detection hashes freshly verified read/check results before large-output
compaction. It retains at most 128 observations per run and recognizes repeats
separated by other reads. Changed output is new evidence even with identical
arguments. Mutations, failures, unknown side effects, and user steering clear
prior evidence. No results are cached or tool executions skipped.

Known file/discovery tools and direct verification commands participate;
command timings and standard Python/Go test-duration lines do not. Arbitrary
shell commands, network observations, and process polling remain outside this
comparison. Repeats use short factual warnings; they do not stop a run after a
few unchanged rounds. The existing 50-identical-call emergency guard remains.
Output comparison also avoids a redundant adaptive reflection model call for
these reads/checks; failure and explicit fixed-interval reflection still work.

## Shorter instructions and intact tool batches

The existing core, extended, coordinator, and worker prompts now emphasize reuse
of available evidence, scoped checks, and continuing workers whose context is
useful. They no longer encourage automatic fresh reviewers or memory lookups on
every task. Generic serial-read coaching messages were removed: a sequence of
new reads is not evidence of waste. There are no new helper model requests.

Measured instruction text lengths (characters, not provider-billed tokens):

| Layer | Before | After |
| --- | ---: | ---: |
| Core | 1016 | 1014 |
| Extended | 508 | 335 |
| Coordinator | 2704 | 980 |
| Thin tool protocol | 548 | 582 |

Core + extended + coordinator is about 45% shorter. This excludes tool schemas,
user instructions, and history; it is not a measured reduction in whole-task
cost or latency. Stable-prefix construction and provider transports are unchanged.

Thin protocol instructions allow independent calls in separate blocks within
one response. The stream consumer now retains every complete sentinel/XML call,
trailing prose, and usage, even when they arrive in the same delta. Previously
it discarded the suffix after the first block. Regression tests cover every
split point for sentinel, XML, and mixed batches, plus a two-tool loop with thin
mode enabled and disabled that needs only one tool round and the final reply.

Pruned results no longer advise re-executing tools. Command exit codes remain
in the compact marker when present in the structured result; no success is
inferred when the status is missing. The canonical stored transcript is intact.

Validation uses deterministic providers and offline tests. Actual local/cloud
model behavior and end-to-end cost still require representative live sessions.


## Inspecting retained output instead of repeating work (2026-09-23)

Compared the local OpenCode, Kimi Code, Pi and DeepSeek Harness snapshots. The
useful common boundary is between retained evidence and its model-facing view.
OpenCode stores oversized output and points to search/ranged reads
(packages/opencode/src/tool/truncate.ts); Kimi stores it per agent before
rendering a preview (agent-core-v2/.../toolResultTruncationService.ts).
DeepSeek's output-retention library separates mechanical byte/item limits from
command exit status and other tool-specific facts. Pi also separates canonical
messages from the view sent to the provider (packages/agent/src/agent-loop.ts).

SuperCLI already had bounded previews and a registry-local output store.
Two missing pieces are now covered by the shared loop/core implementation:

- Failed tools retain their returned diagnostic text when the inline failure
  summary omits it. Exit status and the existing short summary remain inline;
  the model can inspect the saved result without rerunning the operation.
  This also covers Go-level execution errors with accompanying output.
- Existing read_output accepts an optional, case-sensitive literal query.
  It searches saved text and returns bounded excerpts with original byte
  offsets; continuation offsets cover remaining matches. This handles
  multi-line or minified output and UTF-8 without another tool or system prompt.

The 32-result / 16 MiB LRU limits remain. Search uses at most eight excerpts and
an 8 KiB payload budget per response (plus labels). Small results are unchanged,
read_output never stores itself, and UI events keep the complete returned
output and error status. Handles remain in memory for this loop; this change
neither adds disk persistence nor recovers text already capped by a tool's own
capture limit. No failure is converted into success.

Offline regression tests run a failed command followed by a search through
its handle in native and thin modes, including both ordinary tool failures and
runtime errors. They assert one command execution, preserved exit status,
retrieved middle-of-output evidence, and bounded model context. Core tests cover
literal matching, Unicode, continuation, excerpt boundaries, cancellation,
expired handles, small-result compatibility and oversized failures.
No live-model speed or API-cost claim is inferred from these fixtures.


## Ordered mixed tool batches (2026-09-23)

SuperCLI already accepts multiple native or thin tool calls per model response.
The mixed-batch fallback now runs contiguous independent read groups together,
keeps commands/unknown mutations as barriers, and batches contiguous delegation
calls according to the configured worker-backend parallelism policy. Repeated
instructions to one retained worker split groups and preserve their order.
Results still enter conversation history in original call order. Known file
conflict scheduling keeps its existing behavior.

A tool_search followed by an invoke_tool envelope in the same response can now
execute after discovery succeeds. Dispatch is checked again at the execution
barrier, without activating tools in advance. Missing discovery, reversed order,
invalid arguments, target verification and restricted registries keep their
normal controls. The recorded call/result pair keeps its original envelope name;
UI events, target checks and failure attribution use the actual tool name.

Offline verification:

- A scripted discovery + execution + final-answer flow uses two model requests
  when discovery and execution arrive together, versus three when split. Native
  and thin modes both pass. This does not predict how often a real model batches
  calls or whether it already knows the target arguments.
- Mixed read/command tests synchronize on completion channels, proving that reads
  overlap within a group and never cross the command barrier. Worker tests cover
  sequential/local and parallel/cloud policies and ordered worker continuations.
- Synthetic scheduling benchmark: eight 5 ms reads around one 5 ms command,
  same work and ordered results, measured ~51.1 ms sequential vs ~17.0 ms grouped
  on this machine. Allocations were ~22.6 vs ~26.9 KB per complete batch. These are
  scheduler/I/O fixtures, not an end-to-end live-model speedup claim.

Reproduce: go test ./internal/agent -run TestMixedBatch -bench
BenchmarkMixedBatchExecution -benchtime=250ms. No system prompt, provider wire
setting, extra model call or new tool schema was added for these changes.


## Cancellation at tool dispatch (DeepSeek Harness comparison, 2026-09-23)

DeepSeek Harness checks cancellation at its tool-dispatch checkpoint
(packages/session/session-checkpoint-policy/src/index.ts) and distinguishes
unstarted calls from calls whose outcome is unknown during recovery
(packages/core/session/src/repair.ts). SuperCLI now applies that distinction
when a turn is cancelled or its deadline expires:

- Check the turn context immediately before executing each tool. Extensions
  that ignore cancellation cannot start a later operation in the ended turn.
- Close every call/result pair, including skipped members of sequential,
  parallel, mixed and delegated batches. A skipped tool gets TOOL_NOT_STARTED;
  an already-dispatched tool that returns the turn's cancellation error gets
  TOOL_OUTCOME_UNKNOWN. The latter may have produced side effects and is not
  automatically retried. Successful results remain successful even if they
  arrive after cancellation.
- End the step after recording the batch, before reflection or another model
  request. Cancellation does not count towards identical tool failures.

This adds no standing prompt, model request, polling or new storage. The usual
path adds a context check at dispatch and checks interrupted errors on return.
The existing session writer retains the outcomes for CLI/GUI resume; worker
loops use the same dispatch path. OpenCode Zen's gate and transport are unchanged.

Regression tests first reproduced execution after cancellation in seven batch
variants. They now assert zero tool executions and zero verifications for those
cancelled batches. A real SQLite session test (native and thin tools) saves the
first mutation, cancels before the second, reloads the transcript and checks
that continuation receives both outcomes without replaying either mutation or
adding a recovery model call. A channel-coordinated parallel test preserves a
successful sibling result beside an interrupted call and an unstarted mutation.

This covers cooperative cancellation, not abrupt process termination or power
loss. Recovery of a hard-crash tail remains a separate issue; no synthetic
success is inferred for an operation without a recorded result. These are
regression fixtures, not measurements of live-provider task completion speed.


## Failed commands remain failures (2026-09-23)

A saved session exposed a failed ctx_execute validation (timeout_ms=120000,
maximum 30000), followed by successful search/diff calls and a final promise to
correct the command, without a rerun. The previous completion guard also treated
any later concrete tool success as recovery from a failed test.

- process_session now maps failed/timeout/nonzero completed exits to Result.Err,
  with an exit status and output tails using the existing ctx_execute formatter.
  The JSON snapshot is retained for UI rendering. Running processes, successful
  exits, intentional stops and listing remain ordinary management results.
- The existing goal completion/verification guard retains identities of failed
  recognized test/build/check commands during a Run. Reading files, editing code,
  git diff, or a different passing check cannot erase one. The same command in
  the same workdir/environment must succeed; timeout/output-limit changes do not
  change its identity. Background completion snapshots are supported too.
- Existing core, worker and coordinator instructions now say to fix and recheck
  failures or report a concrete blocker, and not finish with promised work.
  These replacements are shorter in characters in all three prompt sources;
  no permanent instruction section, classifier call or automatic command retry
  was added.

The completion guard applies to explicit goal actions, not to free-text final
answers. Shell scripts and arbitrary commands are not inferred to be tests;
known direct test/build invocations are recognized conservatively. This improves
error signaling and guidance, not a guarantee that every model will fix every
error. No claims of live-provider behavioral improvement are made from fixtures.

Regression coverage includes the rejected-timeout/search/diff sequence, a
corrected successful retry, a different check/workdir, running and failed
background snapshots, and an actual subprocess exiting with code 7. Tests wait
for the process completion event before reading its final snapshot.


## Useful read results instead of avoidable failures (2026-09-23)

Saved session 3c6a4df38eb40a77 contains five rejected read_output calls across
two model responses: limits 14000 (offset 3072) and 12000 (offset 0), rejected
by the schema maximum of 8192. The runtime already capped read/search output;
schema validation prevented that code from serving any evidence. Oversized
integer budgets now reach that existing cap. The model receives up to 8 KiB
of payload plus labels and a continuation offset, using the same retained
handle. Types, handles, offsets and query validation still apply.

The local OpenCode snapshot provides useful precedents in
packages/opencode/src/tool/read.ts: optional read limits with runtime output
truncation, and up to three nearby filename suggestions on a missing read.
DeepSeek Harness has bounded read rendering too, but its argument parser rejects
limits above maxLimit; it is not the precedent for accepting oversized budgets.

SuperCLI now adds up to three existing similar basenames to missing-file errors
from read_lines, read_context and individual read_many entries. Matching uses
case-insensitive filename stems containing one another, with the same extension.
Only the first 512 entries of the already-resolved parent directory are inspected;
there is no recursive search, symlink following or alternative-file read.
Large directories can yield incomplete suggestions. Missing parents and unrelated
names retain the original error. Successful reads incur no added directory I/O.
The short filesystem error format now preserves its underlying error identity.

Regression coverage replays both observed limits through Registry.Execute and
native/sentinel agent flows, verifies unchanged payload caps and lossless UTF-8
paging, and checks missing-file recovery, cancellation, sandbox boundaries and
symlinks where supported. These are offline fixtures: five avoidable rejected
calls are not a claim of five saved model turns or a measured provider speedup.
No system instructions, extra model request, persistent cache or new tool were
added. The common tools serve local/cloud models, workers, TUI and WebGUI.


## Reuse the catalog during context budgeting (2026-09-23)

The provider already freezes its thin-tool catalog in the stable prefix. However,
pruning and compaction budget checks rebuilt that catalog before every request,
including parsing schemas, sorting signatures and concatenating the same hints.
The checks now reuse the existing frozen string. This also fixes an estimate
mismatch: a late tail-tool registration could increase the estimated prompt even
though it did not change the catalog actually sent. Explicit registry replacement
still invalidates the frozen catalog; non-hoisted/dynamic modes keep their behavior.

Tool-definition token estimates now count the original fields directly instead
of copying name + description + schema into a temporary string first. The
heuristic and message-framing cost stay identical, including unusual Unicode
whitespace in external tool definitions. No extra model text, prompt instruction,
request, cache allocation or disk persistence is needed by either optimization.

Offline benchmark on Windows/AMD Ryzen 7 5800X3D, medians of three 300 ms samples:

| Fixture | Before time | After time | Before allocated bytes | After allocated bytes |
| --- | ---: | ---: | ---: | ---: |
| Warm thin context preparation | 2.129 ms | 0.110 ms | 1,065,816 | 84,570 |
| Warm native context preparation | 0.103 ms | 0.082 ms | 127,376 | 88,390 |
| 48 large-schema tool estimates | 0.220 ms | 0.230 ms | 393,219 | 0 |

Context fixtures have 15 core tools, 48 tail tools and 40 user/assistant pairs.
One iteration performs two budget estimates plus request assembly and its estimate.
The large-schema fixture isolates allocation reduction; its CPU time did not
improve in this run. These are client-side preparation costs, not a 19x speedup
of model inference or complete tasks. Provider-visible catalog bytes remain the
same. No provider transport or OpenCode Zen gate was modified.

Reproduce with go test ./internal/agent ./internal/llm -run '^$'
-bench 'Benchmark(ContextPreparationWarm|ToolBreakdownLargeSchemas)$'
-benchmem -benchtime=300ms -count=3. Regression tests cover frozen-catalog accuracy,
registry replacement, route/mode changes and equivalence of 729 whitespace-field
combinations. Full go test ./... and go vet ./... pass.


## Await background command completion without polling (2026-09-23)

The saved sessions contain one process_session start (session 640fd11f345b952b,
sequence 1077), followed by conversation rewinds and later inspection through
ordinary commands. This is evidence of background-command use, not a measured
polling loop or proof that waiting would have repaired the interrupted turn.
The concrete API gap was that process_session exposed start/poll/write/resize/
stop/list, while its manager already had an internal completion channel.

Existing process_session now accepts action=wait with id. It blocks on that
channel until the process finishes or the caller's context is cancelled; there
is no periodic status read, timer loop, subprocess probe or model call while
waiting. Process lifetime limits and explicit stop still control termination.
Cancelling the wait does not stop/restart the process or consume unread output.
Normal callers can start a finite build, do independent work, then wait once.
Servers and interactive programs still support inspection, input and stop.

A completed wait returns all unread output still retained in the existing
64 KiB-per-stream buffers, with omitted-byte counts for anything already evicted.
The agent's shared output store supplies its existing bounded model preview and
retrievable handle. This preserves the final diagnostic beyond a 12 KiB poll
chunk without increasing the model's output cap. Completed nonzero exits and
timeouts use the existing failure path; completed waits do not replay consumed
output. Processes and output buffers remain in memory, with no new disk storage.

No new tool or system instruction was added. The existing tool description was
rewritten more briefly and its action enum gained wait. The common implementation
serves CLI, WebGUI and workers on both local and cloud backends. Provider transport
and the special OpenCode Zen path are unchanged.

Tests cover completion-channel wakeup, cancellation with later recovery, manager
shutdown, missing ids, bounded retention, final diagnostics and failure status.
Process/PTY interactive tests now await completion rather than sleep/poll loops.
Scripted native and sentinel flows run a real helper process, execute start and
wait, and expose success/failure to the next model request (three requests total,
including the final answer). This demonstrates the available flow, not a guarantee
that every real model will select wait or a live-provider speed measurement.
Full go test ./... and go vet ./... pass.

## Command capture before preview truncation (2026-09-23)

The registry's large-result store could only retain the output that reached it.
Previously ctx_execute wrote streams to temporary files, kept their final
16/4 KiB by default, then deleted the originals. An earlier compiler error was
therefore unavailable even through read_output.

The runner now drains stdout/stderr into bounded in-memory head/tail buffers,
retaining up to 1 MiB of content per stream (plus an explicit omission marker
above that). The UI/JSON preview keeps its existing limits. A separate,
non-serialized RetainedText supplies the registry's existing 32-entry/16-MiB
store; read_output can search or page this evidence without running the command
again. When the capture limit is exceeded, the stored JSON marks the stream
truncated; it never claims to contain the missing middle. No capture files,
background cleanup, added model calls, tool schemas or system instructions.

Failed commands use the same 2-KiB-per-stream summary budget, split between
the early diagnostic and final status when a larger capture is available.
Exit status and timeout remain explicit. A one-second WaitDelay bounds inherited
output pipes; incomplete capture is reported as an error. Very large output
uses more bounded RAM than the old tail reader, while eliminating unbounded
temporary-file writes. This is not a claim of universally faster command execution.

Regression tests cover real subprocess success/failure, retained early evidence,
UTF-8 cuts, overflow markers, timeout, inherited pipes, store bounds and both
native/sentinel model routes. See [the Qwen evaluation](evals/2026-09-23-command-capture-qwen.md)
for live measurements, including unsuccessful pilot trials and remaining reads.

## Correct recovery of rejected edit batches (2026-09-23)

Session inspection found eight patch_file errors saying that earlier changes
matched and the model should resend the failed change alone. PatchFile is
all-or-nothing for validation: those earlier matches existed only in memory,
and no changes were written. Following the old hint could omit valid edits or
fail again when the corrected change depended on an earlier replacement.
For example, session 640fd11f345b952b, seq 465 contains this contradictory hint.

The error now identifies the failed index and requests the complete corrected
batch, explicitly distinguishing in-memory matches from saved changes. The
first-change failure also retains the full-batch requirement. This alters only
failure feedback; successful output, tool schemas and system prompts are unchanged.
Tests verify rejection at the first/middle/last change, unchanged file/hash/write
count after failure, full corrected retry, dependent replacements and CRLF
normalization. A scripted production-loop regression covers native and sentinel
routes. No measured live-model turn reduction is claimed for this change.

## Skip unused vocabulary loading for local estimates (2026-09-23)

The TUI falls back to CountTokensEstimate when backend output usage is missing.
That function initialized cl100k_base before checking whether the model used
that counting path. A Qwen/Llama estimate therefore paid a one-time unused
vocabulary initialization; the dependency can also fetch the vocabulary when
its cache is absent. Model-family selection now happens before initialization,
as it already did in CountTokens. Numeric estimates and tokenizer-backed
behavior stay unchanged.

An isolated subprocess test supplies a counting, non-network loader: local/
unknown model estimates and empty text perform zero loads, while a later
tokenizer-backed request still attempts exactly one load and falls back on
failure. This avoids resetting sync.Once or adding a production testing hook.
The change removes unnecessary initialization; it does not alter inference,
KV precision, context size or provider instructions.

## Stream targeted reads and preserve every batch section (2026-09-23)

read_lines and read_context used to load and split the entire file before
selecting a range. They now share a streaming reader with read_many. It keeps
only selected lines and stops at the requested end; skipped/huge lines use
32-KiB chunks with cancellation checks. Model tools keep 1800 bytes per line
plus an explicit omission marker; library ReadLines/ReadContext retain their
full-line contract. The existing first-8-KiB binary/UTF-16 guard, legacy-byte
handling, CRLF semantics, range limits and errors remain covered by tests.
read_many now uses that same guard and no longer silently deletes invalid
legacy bytes. EOF exactly at a buffer boundary no longer loses the final line.

Large read_many responses previously lost complete middle sections when the
registry applied its generic head/tail preview after the tool's own truncation.
The tool now supplies a structured ModelPreview: every requested file gets a
header and a share of the same 4-KiB model preview budget. Short bodies release
unused space to others; long UTF-8 paths are shortened within the header budget.
Partial errors remain visible. Small results remain byte-identical.

RetainedText saves numbered output before the batch/UI truncation, so read_output
can inspect omitted middle sections from the existing bounded in-memory store.
The per-line and per-file rendered-output caps still apply; this is not an
unlimited source-file snapshot. Retention may use roughly 0.8 MiB for a maximum
batch, within the existing 16-MiB/32-entry store. The store remains transient;
handles expire on eviction or process exit. No extra file reads, new storage
locations, tools, schema changes or system-prompt instructions were added.
CLI, GUI and workers share the same implementation for local/cloud providers;
OpenCode Zen transport was not changed.

Regression tests exercise every header/error through both native and sentinel
model routes, retrieval of a middle section after deleting its source fixture,
UTF-8 budgets, tiny unchanged outputs, cancellation, huge/skipped lines, exact
buffer-boundary EOF, encoding classification and context ranges past EOF.
Scripted model tests validate plumbing, not actual model decisions or turn savings.

A local Go benchmark compares the prior whole-file algorithm against ReadLines
for 20 lines of a 16,000,000-byte fixture (100,000 lines of 160 bytes). Windows,
AMD Ryzen 7 5800X3D, Go 1.26.2; three runs of 20 iterations each, warm OS cache.
Median times; approximate allocated bytes per operation:

| Position | Whole-file baseline | Streaming | Baseline allocation | Streaming allocation |
| --- | ---: | ---: | ---: | ---: |
| Start | 4.080 ms | 0.0448 ms | 33.62 MB | 38.3 KB |
| Middle | 4.135 ms | 1.825 ms | 33.62 MB | 38.3 KB |
| End | 4.854 ms | 3.685 ms | 33.62 MB | 38.3 KB |

Run: go test ./internal/tools/fileops -run '^$' -bench '^BenchmarkReadRangeStreaming$' -benchmem -benchtime=20x -count=3.
Raw local results: .tmp/read-stream-benchmark.json. These measurements cover
file I/O and allocation, not provider inference, real agent turn counts or bill
reduction. Late ranges must still scan their prefix; small files have a fixed
buffer overhead. Fewer rereads/model turns are a plausible benefit of preserving
batch evidence, not a measured live-model result of this change.

The proposed richer successful patch confirmation is deferred: the tool already
returns replacement count, changed status and before/after hashes. Always adding
source excerpts would add model input on every successful edit; recorded follow-up
reads also include legitimate formatting and verification. Its net turn/token
benefit needs a separate isolated comparison before changing that output.

In the twelve-item regression fixture (eleven files plus one missing file),
the old model preview had 4322 bytes and only two file headers. The new preview
has 4096 bytes including its retrieval notice and all twelve headers; 94095 bytes
of bounded evidence remain retrievable. Full go test ./... and go vet ./... pass.

## Search source files instead of overlooking them (2026-09-23)

The logs show a concrete false-negative loop, independent of prompting:
session 640fd11f345b952b, seq 888 reads windows_detect.zig containing
DetectedSystem; seq 889 searches src for that identifier (among alternatives),
then seq 890 returns no matches. Searching the project root at seq 891 instead
fills seq 892 with .zig-cache entries. More source-only searches at seq 900,
934 and 947 also return no matches despite the neighboring Zig reads.
Session 48cc2fba43e7c055 seq 1135/1136 and 1144/1145 shows nonexistent comma-joined
paths being presented as no matches, followed by broader searches and an
attempt to run an unavailable rg at seq 1148/1149.

The Go fallback had a short extension whitelist excluding .zig, .zon, .cmd,
.bat, .ps1, HTML/CSS and extensionless build files. It now scans regular text
files regardless of extension, with an 8-KiB NUL sniff to skip binary/UTF-16
content. This is a bounded text heuristic, not a full encoding detector or
full ripgrep ignore-file implementation. It does not follow file symlinks.
Missing roots produce not_found; a missing comma-joined path explains that path
accepts one existing file/directory. Existing names containing commas stay literal.
Scanner/read errors retain partial evidence and report an incomplete search;
a line beyond the existing 1-MiB scanner cap can no longer silently end a scan.
Cancellation is checked during walking and scanning.

The shared ignore set now excludes Zig build/cache directories, .tmp and
portable supercli-data. It is also used to construct rg exclusions. Ignore
checks apply within the requested root, not to its ancestors, so workspaces
under a directory called .tmp/build still work. Workspace-relative root
resolution no longer joins a relative WorkDir twice. Results use paths relative
to the tool's workspace (not the narrowed search root); allowed external paths
stay absolute. rg always includes the filename, including single-file searches.
A reader error cancels rg before Wait, preventing an unread output pipe from
blocking process completion; a cancelled search cannot report no matches.

The Go scanner reuses its reader and scan buffer across files and matches RE2
against bytes; ordinary nonmatching lines no longer allocate strings. A controlled
64-file Go corpus (512 ordinary lines per file, absent target) compares the old
scanner algorithm with the new fallback, with identical searchable content.
Windows, Ryzen 7 5800X3D, Go 1.26.2, three runs of 20 iterations, warm cache:
median 6.051 ms -> 4.954 ms (~18% lower), allocated bytes 5.96 MB -> 0.160 MB
(~97% lower). The new path includes binary sniffing and root validation.
Wider language coverage can mean opening more files in other repositories;
this benchmark is not a promise that every search or model turn is faster.
Run: go test ./internal/tools/search -run '^$' -bench '^BenchmarkSearchFallbackBuffers$' -benchmem -benchtime=20x -count=3.
Raw output: .tmp/search-fallback-benchmark.json.

Tests cover Zig/scripts/build files, binary sniffing, cache saturation, missing
and comma-containing roots, relative workspace resolution, cancellation, oversized
lines, external paths, and scripted native/sentinel search-then-read flows. A helper
executable checks rg arguments, single-file formatting and a pipe-overflow failure;
this host has no installed rg, so these are subprocess protocol regressions rather
than a real-ripgrep benchmark. No tool schemas, system prompts, provider settings,
OpenCode Zen handling, retries or model punishment counters were added or changed.

A replay over the latest 178 non-error saved search_code outputs removes only
their own session workspace prefix at the start of result lines: 535793 ->
433823 bytes (~19% less text). Five of those saved outputs contain .zig-cache
hits. This measures formatting on historical stored previews, not a reduction
in live provider tokens, inference time or bill. The output-store threshold can
also change which results are inline. Audit: .tmp/search-output-replay.json.

Validation: full go test ./... (64 packages), go vet ./... and git diff --check
passed after the source-search regressions.

## Append streamed tool arguments without repeatedly copying the prefix (2026-09-23)

Investigation started from four saved empty-argument read_many/search_code calls
in session 97f11945104d6cb4 (assistant seq 210 and 245; results 211/212 and
246/247). The saved history contains {} and missing-required-field errors, but
there is no retained raw stream for these calls. That evidence cannot distinguish
model output, gateway behavior and prior normalization. This change does not
claim to fix those empty calls and does not synthesize missing arguments.

A separate concrete cost was visible in both OpenAI-compatible and Anthropic
native streaming parsers: each argument fragment used string += fragment,
copying the entire accumulated prefix again. Large patch_file arguments split
into many deltas therefore generated quadratic copy/allocation work.
A pointer-owned strings.Builder now appends fragments with amortized linear
copying. Completion produces the same immutable ToolCall string. Metadata,
call ordering, empty inputs, partial inputs, finish behavior and Anthropic's
existing {} placeholder handling are preserved. Tool schemas, prompts, model
requests, retry behavior and special OpenCode Zen request/header handling
are unchanged. Text/sentinel calls and the Responses parser are not part of
this optimization.

The native OpenAI regression also exposed a different bug: usage on a later
frame containing choices was ignored after the first finish event. It now emits
that accounting without replaying the completed tool calls or finish. The
pre-change test failed with usage=nil; the corrected parser preserves the
reported input/output totals. This improves accounting rather than reducing
the tokens billed by the provider.

A production Anthropic SSE-parser benchmark (including JSON parsing, no network
or model) uses a 68,655-byte argument object fragmented into roughly 32-byte
UTF-8-safe pieces. Three runs of 20 iterations per variant, Windows/Go 1.26.2,
Ryzen 7 5800X3D. Large-call allocated bytes fell from about 82.43 MB to 2.89 MB
(~96.5% less); median parser time 51.32 ms -> 6.25 ms. Timing varied: old
19.56-62.79 ms, new 6.07-8.48 ms, so allocation reduction is the more stable
result. The 251-byte small-call case stayed around 0.04-0.05 ms with ~79-80 KB
allocated. These are total allocations, not resident memory or GPU KV usage,
and no whole-session speed, model-turn or billing reduction is inferred.

Run: go test ./internal/llm -run '^$' -bench '^BenchmarkAnthropicToolStream$' -benchmem -benchtime=20x -count=3.
Raw local results: .tmp/tool-stream-before.json and .tmp/tool-stream-after.json.
The original late-usage regression failure is saved separately in
.tmp/tool-stream-baseline-regression.json.

Tests cover large Unicode/escaped-code payloads in both provider parsers,
interleaved sparse tool indices, duplicate finish frames with late usage,
Anthropic calls ending out of index order, empty/complete/partial initial
arguments and immutable completed snapshots. No live model edited the repository.

Validation: all 64 tested packages, go vet ./... and git diff --check passed.

## Shared model handoff and search neighborhoods (2026-09-23)

Long model switches now use the shared summary/projection mechanism in GUI and
TUI, preserving the two recent user turns and full archived transcript. Small
contexts and same-model continuation add no summary call. Large tool arguments
are bounded in summary input. search_code can return optional numbered context
lines without another read call; its serialized tool definition is smaller.

See [the evaluation report](evals/2026-09-23-context-handoff.md) for the policy,
measurements, tests and limits, including the one-time summary inference cost.

## Refreshed memory without rewriting the conversation prefix (2026-09-23)

GUI memory and folder-index snapshots now use the shared agent request tail.
An updated fact no longer changes the bytes ahead of the conversation; no
additional model call or instruction is added. One local Qwen3.8-27B synthetic
test (~9.6k input tokens) measured 10.488 s -> 0.381 s to first text after changing
the fact, with identical prompt sizes and correct answers in both layouts.
The initial request still took ~10.5 s; this is not a whole-task speed claim.
See [the evaluation report](evals/2026-09-23-context-prefix.md) for the fixture,
limitations, raw evidence and tests.

## Keep discovered tools available across GUI turns (2026-09-23)

Explicit tool_search discoveries now survive fresh loops and session resume.
A previously discovered tool no longer fails only because the GUI reconstructed
its registry. The shared loop stores a small name snapshot in the portable
session database; unchanged state causes no write. Automatic promotions remain
separate. No prompt instruction or helper model call was added.

See [the regression report](evals/2026-09-23-tool-discovery.md) for the reproduced
refusal, continuation tests, session lifecycle and limitations.


### Startup repository context (2026-09-23)

Optional non-Git preflight uses a bounded, explicitly labeled file sample. In a 12000-file synthetic tree, median collection time fell from 468.65 ms to 25.64 ms; this is not an end-to-end model latency measurement. Git status overlaps identity reads, and GUI cancellation reaches the collector. Project facts and prior session results still reach the first request after restart. See [measurement and validation](evals/2026-09-23-startup-preflight.md).


### Retained outputs across restart (2026-09-23)

Large tool outputs now use unique handles and a bounded portable SQLite cache. A fresh GUI loop or resumed session can inspect old evidence without rerunning its producer; opening a session does not preload full results. Ordinary inline results incur no additional I/O. A 128 KiB retained result took about 1.5 ms to save in the local microbenchmark. See [behavior, limits and regressions](evals/2026-09-23-output-resume.md).

### Native reasoning and lightweight startup (2026-09-23)

Native reasoning now survives completion, persistence and resume in provider format,
separately from GUI/TUI display text. Ten paired live comparisons on Qwen, MiMo
and Muse favor keeping it in this small sample; all 20 implementation arms passed,
with individual counterexamples and no universal speed or cost guarantee. Signed
Anthropic state is guarded against changes to its original request prefix.

Light turns now continue into discovered tools within the same request flow,
restoring project instructions without asking the user to repeat the task. The
replacement route/tool descriptions are shorter; no helper inference was added.
See [measurements, limitations and regressions](evals/2026-09-23-native-reasoning.md).

### Optional removal of completed reasoning

GUI/TUI settings and `/reasoning-history keep|drop|default` expose the shared
`discard_previous_reasoning` preference. Default: keep. DROP changes only the
request projection, preserving the archive, visible replies and required tool-call
reasoning. It takes effect at the next turn and adds no inference or instruction.
Tests cover reversibility, an in-flight preference change, interrupted exchanges,
config merging/persistence and GUI restart. See [configuration](configuration.md#previous-reasoning-in-conversation-history).

## Focused source search (2026-09-23)

search_code now accepts an optional include glob and reports when its match
limit stopped the search. The Go fallback filters before opening files; rg
receives the same filter. This avoids documentation crowding out source hits.

On a synthetic source lookup, Muse used 4 instead of 5 model turns in two paired
runs and about 32% fewer total input tokens. Wall time did not consistently
improve; Qwen showed no turn reduction. The schema grew by 30 serialized bytes,
with no new system instruction or helper inference. See [measurements and
limits](evals/2026-09-23-search-focus.md).

## Session list activity and query cost (2026-09-23)

Old conversations move to the top after a new message is saved, before the model
finishes. Date groups follow latest activity. Superseded list requests are
cancelled and stale responses cannot overwrite the current project list.

Project lists now use the existing activity index instead of grouping full
message histories. For 60,000 synthetic messages, the median 40-session query
fell from 50.675 ms to 0.826 ms. This measures the local query only; no inference,
prompt text or model tokens were added. See [behavior and validation](evals/2026-09-23-session-activity.md).

## Avoidable tool-envelope repair turns (2026-09-23)

The dispatcher now accepts supported visible built-ins such as remember and task
without asking the model to resend the same call directly. Core eligibility uses
the existing schema sets, and native, dotted and top-level envelope arguments
share a decoder. Ambiguous fields remain errors; normal target validation,
verification and concurrency rules apply.

Twelve controlled replay cases dropped from 3 model calls to 2, with exactly one
target execution. No new prompt text, schema or helper inference was added. This
is a reduction in avoidable repair turns, not a universal latency percentage.
See [replay method and safeguards](evals/2026-09-23-invoke-economy.md).

## Skip impossible search subtrees (2026-09-23)

The built-in search now prunes entire directories that cannot match a focused
include glob. On a synthetic tree with 2048 unrelated files, median search time
fell from 12.406 ms to 0.266 ms and allocations from 1.44 MB to 0.11 MB, with
identical results. This is local tool latency; broad searches and model inference
are not covered by that speedup. No prompt, schema, helper model call or runtime
dependency was added. See [measurements and limits](evals/2026-09-23-search-pruning.md).

## Cheaper request preparation (2026-09-23)

Long text estimation now uses optimized byte counts while short names retain the
cheap scalar path. Tool definitions are assembled without unused intermediate
partitions. Token counts, tool order and request schemas are unchanged.

In paired synthetic tests with 97,460 estimated history tokens, preparation fell
from 296–367 us to 48–54 us, with 41–51% fewer allocated bytes. This measures local
preparation only; it adds no model call and changes no token charge. See
[method, parity tests and limitations](evals/2026-09-23-request-estimate.md).

## Avoid redundant TUI streaming work (2026-09-23)

The TUI shares one append buffer for live text and skips Markdown/layout on timer
ticks when the text, completed history and spinner frame are unchanged. New
chunks remain visible immediately. Copied model snapshots and reset paths are
covered by regressions.

In a paired synthetic fixture, 180 text fragments plus 180 redundant frame
updates fell from 106.563 ms to 57.404 ms. An unchanged 32 KiB frame fell from
3.308 ms to 0.026 ms. These are terminal event-handler measurements, not model
or GUI speedups. See [method and limits](evals/2026-09-23-tui-stream.md).

## Lower tool discovery cost (2026-09-23)

Lexical tool_search scoring now visits description words directly and shares
query-local deduplication state across tools. Comparison sorting replaces
insertion sorting. Results, ranking, schemas and activation remain the same.

Paired synthetic tests measured 0.153 ms -> 0.080 ms for 64 tools and
1.688 ms -> 0.464 ms for 512 tools, with 73–83% fewer allocated bytes.
This improves description lookup in searchers without an FTS index or when FTS
returns no hits/errors. Exact-name lookup and successful FTS searches are
unchanged. No model tokens or calls are added. See [method, parity checks and
limits](evals/2026-09-23-tool-discovery-cost.md).

## Avoid rereading entire sessions for project memory (2026-09-23)

GUI post-turn capsule maintenance now reads the first useful user message and
last eight useful dialogue messages through one SQLite snapshot. Tool payloads
are excluded before entering Go. Text selection and the resulting memory note
remain identical to the full-transcript path.

For a synthetic 1200-message session, the complete update fell from 18.277 ms
to 1.921 ms, with allocations falling from 8.83 MB to 63 KB. This measures local
post-turn memory maintenance; it adds no inference or prompt text. Sparse
histories may require more scanning. See [method and correctness checks](evals/2026-09-23-session-capsule-cost.md).

## Lighter GUI session statistics (2026-09-23)

For sessions with recorded provider usage, statistics now count valid messages
without loading plain transcript text into Go. Existing decoding still checks
parts and tool calls. Older sessions retain text-based context estimation.

In paired synthetic tests with 1200 messages, Engine.stats fell from 16.160 ms
to 10.066 ms; allocated bytes fell from 9.08 MB to 1.05 MB. This measures local
statistics preparation, not model latency. No prompt, inference or persistent
counter was added. See [measurement and compatibility checks](evals/2026-09-23-stats-read-cost.md).


## Fewer repository-discovery rounds (2026-09-23)

list_dir can return a bounded overview up to four levels deep, with shared
search exclusions, explicit coverage limits, and one global entry cap.
Default one-level output is preserved. Its serialized tool definition is
four bytes shorter. Unknown-tool errors now suggest available tools instead
of incorrectly asking the model to fix JSON and repeat an unavailable call.

On a synthetic live explore task, Muse needed 4 instead of 5 model steps and
1–3 instead of 8 directory listings, with lower total input. Cloud wall time
did not consistently improve. Qwen also used fewer steps but chose search,
so that difference cannot be attributed directly to recursive listing.
All nine follow-ups to existing workers answered correctly in one step without
rereading files. No helper inference or persistent instruction was added.
See [method, all runs and limitations](evals/2026-09-23-exploration-economy.md).


## Reuse worker observations at handoff (2026-09-23)

A worker now attaches bounded tool observations to its final report: at most
1 KiB inline for tiny evidence, otherwise a reference through the existing
read_output store. Reasoning and internal conversation are excluded, historical
state is labeled, and a continuation without new observations adds nothing.

In a controlled live historical-value task, final Qwen and Muse each answered
correctly in one completion with no tools or further worker inference.
Baselines usually needed three total model calls; one Muse baseline failed to
recover the old values. A reference-only prototype worsened Qwen cost, so tiny
observations are passed inline. This adds bounded evidence bytes, not a new
instruction or helper model. Larger persisted attachments added about 3.4 ms
in the local 50 KB benchmark. See [all runs, method and limits](evals/2026-09-23-worker-handoff.md).

## Filename discovery without content scans (2026-09-24)

search_code can list file paths with include and no query. It uses the shared
pruned metadata walk without opening file contents or launching rg. Empty files
are included and a global result limit is reported explicitly. No new tool or
system instruction is added; the serialized definition shrinks 867 to 853 bytes.
On a 256-file fixture the tool median falls 18.343 to 1.833 ms versus the existing
Go content scan, with 46.5% less output. Live Qwen/Muse adoption works, but total
latency and token savings remain mixed; redundant model work is not eliminated.
See [experiment and limitations](evals/2026-09-24-file-discovery.md).

## Single-pass search neighborhoods (2026-09-24)

search_code with context now reads separated neighborhoods of each file through
one buffered pass, retaining only selected lines. On a 100,000-line fixture with
20 distant matches, whole-search time falls 30.940 to 7.960 ms and allocation
bytes fall 883,078 to 228,587. Output replay is byte-identical; schemas and prompts
are unchanged. Single-range controls are approximately stable, with a measured
2.7 us early-read increase. A grouped read_many experiment was rejected because
it slowed different-file batches; its original implementation is retained.
See [measurements and rejected variant](evals/2026-09-24-read-windows.md).

## Coalesced saved-output restoration (2026-09-24)

Parallel read_output requests for one uncached handle now share its in-flight
persistence read within an OutputStore. Waiters cancel independently; a live
waiter retries after owner cancellation. Errors and panic release waiters without
leaving a stale in-flight entry. SQLite scans directly to string, removing one
full-output copy. No prompt, schema, cache limit or provider request changes.
A synthetic 1 MiB result read by eight simultaneous callers measured 8.891 to
0.495 ms and 25.30 to 2.11 MB allocated; database reads fell from eight to one.
Memory hits remain around 18 us per eight-reader batch and do no database work.
See [experiment and scope](evals/2026-09-24-output-restore.md).


## Copy only projected image parts (2026-09-24)

Provider preparation no longer copies text/reasoning Parts just to check for
images. It allocates a media view only when an image needs transformation, and
keeps independent snapshots of active image refs. Message values and prompts
are unchanged; no cache or inference is added.

In a controlled 1000-message synthetic history, context preparation measured
185.739 to 125.431 us and 308,488 to 145,791 allocated bytes. The media helper
alone needs zero allocations without images. Short-history timing controls
were mixed; these figures do not measure end-to-end provider latency.
See [measurements, controls and snapshot checks](evals/2026-09-24-media-projection.md).


## Cheaper tool-call/result validation (2026-09-24)

The pre-request protocol guard now uses one table for global ID uniqueness and
pending results, removing a second map and scan per tool batch. Repair behavior
is preserved. In a synthetic 32-batch / eight-call history, validation measured
48.931 to 27.607 us and 109 to 13 allocations. The common single-call case also
improves, while histories without tools remain unchanged.

Whole-request encoding controls have mixed timing results; the measured gain is
in validation and temporary allocation, not model latency or prompt cost.
Three provider builders produce the same fixture request hashes before/after.
See [measurements and compatibility checks](evals/2026-09-24-tool-pair-validation.md).


## Reuse already assembled text (2026-09-24)

Text projection now reuses a single existing string and reserves one buffer
when several fragments need joining. Exact separator, image and reasoning
behavior is preserved. In an 80-message Chat Completions fixture, one-Part
history allocation falls 601,350 to 386,293 bytes; eight-Part history falls
1,049,374 to 601,350 bytes. Chat encoding also improved in both sample pairs.
Other provider builders had mixed timings, including slower Anthropic samples;
additional alternating controls did not reproduce a consistent slowdown.

Nine request fixtures retain identical lengths and hashes. No prompt, model
call, cache or provider token cost is added.
See [all measurements and compatibility checks](evals/2026-09-24-text-assembly.md).


## Reuse recent evidence and avoid lookup delegation (2026-09-24)

The hard orchestrator can search/read directly and its discovery tool now stays
within its own registry. Sparse searches automatically return a small answer
neighborhood. Up to 4 KiB of recent complete tool exchanges survive normal
completed-history omission; larger/older evidence stays retrievable.

In an explicit historical-value fixture, model calls fell 3 -> 1 on Muse and
2 -> 1 on Qwen, with zero retrieval tools after the change. Both still read the
file when asked about its current changed state. Prompts were rewritten slightly
shorter, but orchestrator schemas and retained evidence add bounded input cost.
Timing and redundant-read results were mixed, including an initial Muse intent
failure. See [the complete comparison and limits](evals/2026-09-24-agent-efficiency.md).


## Preserve successful search misses (2026-09-24)

Search verification no longer turns "no results" / "not found" prefixes into
errors. This also fixes real hits in files such as not found.go. Backend errors
and blank-result validation remain intact. The change removes false failure
state and avoidable recovery work without adding prompt or model cost.
See [regression evidence and scope](evals/2026-09-24-search-result-verification.md).


## Preserve moderate batch-read evidence (2026-09-24)

Complete captured read_many batches with multiple successful items now stay
inline up to 12 KiB. Single, partial, failed and larger batches keep their
compact previews; other tools retain their previous limits.

In a controlled continuation with distinct fixture values, Qwen needed one
model call instead of two (14.785 -> 7.251 s). Muse's truncated-view answer
incorrectly claimed the requested values were absent; the complete view produced
the correct answer. Head-only controls show the cost: more input tokens without
a necessary call saved. No prompt/schema or classifier inference was added.
See [all pilot and final measurements, controls and limits](evals/2026-09-24-batch-read-turns.md).

## Large search hit previews (2026-09-24)

Large location results now preserve short middle hits and show bounded excerpts near matches instead of only a generic beginning/end preview. The existing 4 KiB preview budget and full saved-result retrieval remain. In the controlled fixture the final model-visible result fell from 4387 to 1327 bytes; Qwen answered in one continuation call, while Muse still reread saved output. No universal latency or turn reduction is claimed. Small results and tool schemas are unchanged. See [experiment, costs and limitations](evals/2026-09-24-search-hit-preview.md).

## Recent mixed tool batches (2026-09-24)

An oversized completed batch no longer automatically removes every small result issued beside a large one. The next-turn projection can keep complete call/result pairs in the existing 4 KiB recent-evidence budget while preserving canonical history, chronological order, active tool chains and native-state safeguards. In the controlled historical-recall fixture Qwen used 3 -> 1 model calls and Muse 2 -> 1; both still reread the changed file for current-state questions. The first request gained 423 bytes of useful history, so current-state input was slightly larger with no call reduction. See [measurements and limitations](evals/2026-09-24-recent-batch-evidence.md).


## History lookup with filenames and paths (2026-09-24)

search_history now quotes punctuation-bearing bare terms before its existing FTS query, avoiding syntax errors for common filenames and paths. Operators, filters and explicitly quoted expressions retain their meaning; no retry, instruction or model call is added. In a controlled continuation, Qwen used 3 → 1 model calls and Muse 2 → 1, while quoted controls stayed at one. All answers were correct. Local preprocessing took about 125 ns for the path fixture and allocates nothing for already valid expressions. This repairs query syntax, not missing index coverage or all retrieval decisions. See [method, controls and limits](evals/2026-09-24-history-query-terms.md).


## Observed end of file in reads (2026-09-24)

Bounded single/context/batch reads now report EOF when they observe it, including an exact requested boundary. Partial ranges stay byte-identical; no full-file count, schema or instruction is added. In a controlled continuation Qwen used 2 → 1 calls after a complete read, and still read extra lines in the partial control. Muse also used fewer calls, though its baseline included unavailable-tool and range mistakes. All answers were correct; wall time was mixed. The marker added 25 bytes to the fixture and local read cost stayed near 0.58 ms. See [all runs, overhead and limitations](evals/2026-09-24-read-eof.md).


## Useful results for oversized read ranges (2026-09-24)

read_lines now serves the first 500 lines of an oversized request and explicitly identifies any unread requested tail. A short file can reach EOF immediately instead of forcing a range-repair turn. The library cap, valid-result text, tool schema and prompts stay unchanged. In a controlled short-file continuation Qwen used 2 → 1 calls and Muse 3 → 1, with correct answers. A longer-file control cost Qwen more input with no call saving; Muse baseline controls were interrupted by HTTP 429. See [all runs, tradeoffs and validation](evals/2026-09-24-read-range-cap.md).


## Complete matching lines in saved-output search (2026-09-24)

read_output search shifts its existing bounded window to include a complete matching line when it fits, keeping byte offsets and pagination intact. No schema, instruction or context limit grows. In a controlled Qwen continuation the initial snippet shrank from 467 to 454 bytes while preserving the needed value; model calls fell 3 → 1 and both controls stayed at one. Muse answered the candidate case but further runs hit HTTP 429, so no paired cloud speedup is established. Local processing remains roughly 1–1.5 µs, with a small short-line overhead. See [all results, scope and limitations](evals/2026-09-24-output-search-lines.md).


## Use available space for uneven batch reads (2026-09-24)

read_many now keeps a complete captured multi-item result when it fits the existing 12 KiB total and 8 KiB per-item limits, even if an equal per-item share would cut one larger file among tiny siblings. In a controlled Qwen continuation, middle-evidence retrieval fell from 2 to 1 model calls (12.984 → 5.905 s), with all answers correct. The head-only control added 1444 input tokens without saving a call; balanced inputs were unchanged. No prompts or schemas were added, and no live cloud gain is claimed. See [measurements, tradeoffs and validation](evals/2026-09-24-uneven-read-batch.md).


## Preserve late matches in search context (2026-09-24)

search_code with explicit context now preserves a match beyond the bounded line head by reusing a same-search excerpt around it. Original matching lines remain retrievable; visible-head and short-line results stay byte-identical. Automatic context skips a long-line neighborhood read whose result would be discarded. In a controlled Qwen continuation the late case used 4 → 1 model calls (21.386 → 2.558 s), while both controls kept one call and identical input-token counts. The baseline included an invalid-handle mistake; an initially broader pilot regressed the head control and was narrowed. No prompt/schema growth or live cloud speedup is claimed. See [all runs, overhead and limitations](evals/2026-09-24-search-context-hit.md).


## Size hidden-history buffers to the visible view (2026-09-24)

VisibleMessages now sizes its output from the hidden flags and accesses only visible messages during rendering. For a roughly 1000-message history with a short visible tail, the native request-preparation fixture measured 96.928 → 36.167 µs and 600806 → 33792 allocated bytes per operation; thin preparation also improved. Outgoing history and token estimates remain identical. Alternating/all-false flag controls have a small extra scan cost, while the normal nil-flags fast path stays allocation-free. No inference or token-cost reduction is claimed. See [benchmarks, controls and compatibility checks](evals/2026-09-24-visible-history-buffer.md).
