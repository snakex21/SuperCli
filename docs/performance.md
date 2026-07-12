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
- programmatic: `stats.Save(path, turns)` dumps the turns as JSON.

**Why ON.** No new knobs — the recorder was already wired by default;
the loop now feeds it every step. Cost is a handful of timestamps per
step, invisible next to a local-model turn.

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

## Recent fixes and experiments (2026-07-11)

- **EvictForBudget threshold fix** (b2a393c): eviction now compares
  against the session cap instead of tokens already used, so history
  is no longer evicted too early.
- **Streaming consume() O(n)** (593a352): the incremental marker
  scanner replaces the quadratic `text += delta` accumulation on long
  streamed answers.
- **Catalog hoist** (`SUPERCLI_CATALOG_HOIST=1`, 8b43f4f, **default
  OFF**): moves the thin-tools catalog into the stable prompt prefix
  so it caches instead of re-evaluating each turn. Stays off until a
  live A/B confirms the cache win outweighs the prefix-invalidation
  risk when the catalog changes.
- **Navigator on the small provider** (fadc051): route classification
  for the navigator runs on the small side provider; awaiting live
  test.
