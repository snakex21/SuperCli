# Worker evidence handoff — 2026-09-23

## Problem and scope

Inspection of USOS session `02e9bba014852afe` confirmed that the coordinator
later relisted directories and reread files mentioned by an explore worker.
The later user question concerned XP details, so these reads are not all
unnecessary. The missing capability was direct access to observations omitted
from the worker's concise report. Asking the worker again costs another model
invocation; rereading files may return a different state.

## Change

Workers collect bounded textual tool observations during each run: tool name,
arguments, output and any error. They do not serialize their prompt, reasoning,
intermediate assistant commentary or whole conversation. Tool discovery and
user-question results are excluded.

- Observations including their historical-state label fit inline only up to
  **1,024 bytes**. This avoids a retrieval turn for tiny evidence.
- Larger observations use the existing parent output store and `read_output`.
  The main prompt receives the report, a short attachment label and its handle.
- At most 32 latest records and 64 KiB of record bodies are kept per run, plus
  small separators/omission metadata. Each output has an 8 KiB head/tail budget;
  argument and error previews are bounded too. Omissions are explicit.
- Snapshots are labeled with worker ID, run and completion timestamp and clearly
  distinguished from current workspace state.
- A continuation with no new observations attaches nothing. Earlier saved
  snapshots remain immutable and subject to existing bounded output retention.
- Normal tasks, `send_message`, failed runs and background notifications share
  this handling. Without persistence, the parent's bounded in-memory store
  serves larger attachments; with persistence, fresh loops can retrieve them.

No system instruction, tool schema, provider branch, helper inference or runtime
dependency was added. Small inline observations require no new database write.
A larger attachment adds one write through the existing portable session store.
The OpenCode Zen transport and protocol were not changed. Fresh reads/checks
remain available whenever current state is needed.

## Controlled live experiment

`scripts/worker-handoff/main.go` uses synthetic files only. The real explore
worker reads `src/retry.go` and `config/runtime.env` and is asked for a
path-only report. The test harness then changes their values. A real coordinator
is asked for values from the worker's earlier observation, with source paths.

Both versions can use `send_message` to consult the existing worker. The changed
version can additionally use automatically attached observations. Models have
only read-only file/search tools, `read_output` and worker continuation.
The model does not modify the fixture; the harness performs the controlled edit.
Providers are local `qwen3.8-27b-uncensored` and free
`muse-spark-1.3-contributor-free`, with reasoning effort low.

Measurements below cover the follow-up, including any worker inference it
triggers. Input tokens sum coordinator and worker usage; they are not adjusted
for provider caching or converted to money. Initial worker inspection is
excluded. Each row is one run, not a statistically stable latency estimate.

| Model / variant | Model calls, including worker | Total input | Follow-up seconds | Correct old values and paths |
|---|---:|---:|---:|---|
| Qwen baseline A | 3 | 5,656 | 48.279 | yes |
| Qwen reference-only prototype | 3 | 7,007 | 48.552 | yes |
| Qwen final hybrid | 1 | 1,734 | 20.708 | yes |
| Qwen baseline B | 3 | 6,310 | 71.351 | yes |
| Muse baseline A | 3 | 6,017 | 13.251 | yes |
| Muse reference-only prototype | 2 | 4,466 | 9.643 | yes |
| Muse final hybrid | 1 | 1,980 | 5.544 | yes |
| Muse baseline B | 2 | 4,924 | 15.685 | no: reported old values unavailable |

The initial reference-only prototype helped Muse but increased Qwen input:
Qwen searched the attachment and also checked the changed files. This motivated
the final size-based inline/attachment behavior, shared by both providers.

The final hybrid runs each used **one coordinator completion, zero tool calls
and zero further worker completions**. Baselines usually asked the worker;
Qwen baseline B additionally reread current files. Muse baseline B reread current
files and failed to use the existing worker to recover old values.

The report handed to the coordinator grew from about 280–285 bytes to
635–640 bytes in the final tiny fixture. This is a deliberate bounded increase
in evidence, in exchange for avoiding another retrieval or inference. It is not
a token-free change. Larger snapshots stay behind a reference.

This specifically tests omitted historical details after a short handoff.
It does not establish a general speedup for all delegations, demonstrate fewer
current-state verification reads, or guarantee that models will always choose
the cheapest path. A sufficient worker report already needs no extra work.
The ordinary-session audit did not justify prohibiting rereads.

## Local overhead

`BenchmarkWorkerEvidenceHandoff`, three 300 ms samples on this Windows host;
medians for preparing the result and passing it through the existing output
store (not model latency or collection of all tool events):

| Case | Median | Allocated bytes/op |
|---|---:|---:|
| Report only | 0.592 us | 400 |
| Tiny inline observation | 1.131 us | 1,273 |
| About 50 KB, parent memory store | 14.507 us | about 116,540 |
| About 50 KB, portable session store | 3.396 ms | about 222,000 |

## Validation

Regressions cover evidence retrieval without another model/file read, a changed
source file, a fresh coordinator registry backed by persistence, absence of
private worker prompt/commentary, no-tool continuation, failed observations,
UTF-8 and size limits, bounded catalog exclusion, tiny inline results and
background notification delivery.

`go test ./...`, `go vet ./...`, and CLI/GUI builds passed. Raw experiment
results, the failed prototype, source snapshots, before-build overlay,
benchmark output and validation logs are retained in `.tmp/worker-handoff/`.
The before overlay restores the original task/continuation/background behavior;
an unused new Worker field/helper remains compiled and has no runtime effect
on that variant.

All experiments and their data remain under the application directory.
