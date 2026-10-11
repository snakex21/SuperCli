# Project startup, cache reuse and compaction safety — 2026-10-11

The accepted changes remove duplicate goal reads during GUI scope creation and
stop failed compaction from silently discarding older task context. A prototype
that moved the tool catalog before project instructions was tested on the live
configured local model and rejected because it slowed a previously cached project.

Thinking controls, summary instructions, generation limits, sampling, context
length, backend cache configuration and VM processes were not changed.

## Accepted: one fresh goal read per consumer

`Engine.goalServiceScope` creates a scoped service without eagerly refreshing its
active goal. Consumers still refresh before reading cached goal state. This
removes a duplicate cold-scope read without introducing a freshness TTL.

Real SQLite query counts for a loop build with an active project goal:

| Query | Before | After |
| --- | ---: | ---: |
| Active goal | 2 | 1 |
| Task progress aggregation | 2 | 1 |
| Visible unfinished tasks | 1 | 1 |
| Total | 5 | 3 |

With no active goal, the duplicate active-goal read changes from two queries to
one. Already-created scopes retain the previous refresh behavior. Cancellation
is checked before a cold/cached return and before publishing a new service.

The tests use a counting connector around the real SQLite driver, with an
independent connection for changes from another application instance. They verify
fresh goal contents, progress updates, project/global scope isolation, cancellation,
and a real SQL error followed by successful retry.

Five rotated, serial baseline/candidate pairs, 100 iterations per case, used the
exact before `engine.go` through a Go overlay. The database was already open and
migrated; scope creation was cold. Fixtures and scope resets were outside the
timer. Timed code did not contain the query-count wrapper or any model inference.

| Ready-database scope creation + refresh + injection | Before median | After median | Reduction |
| --- | ---: | ---: | ---: |
| No active goal | 0.234 ms | 0.161 ms | 31.2% |
| Active goal, 5 tasks | 0.402 ms | 0.297 ms | 26.0% |
| Active goal, 1000 tasks | 0.982 ms | 0.575 ms | 41.4% |

Allocations in these paths fell from 132/326/330 to 76/234/235 respectively.
This is a small local improvement: it saves roughly 0.07–0.41 ms, not seconds of
model latency. Full loop creation measured 3.94/4.09/4.47 ms after the change;
warm controls and unrelated startup work remain in those totals. These numbers
do not establish a whole-preflight or generation speedup.

## Accepted: preserve history after an unusable summary

Previously, an automatic or context-overflow compaction attempt could silently
fall back to hiding older user turns when its configured summarizer failed,
returned nothing, or failed to reduce the context. The turn could then continue
without older requirements. The fallback now applies only to legacy loops that
have no summarizer configured. A configured summarizer's error stops the turn
before a subsequent main-model request and preserves the existing projection.

The same protection applies to cancellation, including a callback that returns
success after cancellation. Tests cover automatic threshold defense, emergency
overflow recovery, native reasoning/evidence, durable session resume, projection
writes, successful compaction, and the legacy no-summarizer path.

New model-produced summaries above the existing 4000-byte cap are rejected after
the stream finishes. Previously `ClampSummary` could remove trailing `State` or
`Pending` sections and present the shortened text as successful task memory.
`ClampSummary` remains available with its original public behavior, but newly
generated summaries no longer use it. The accepted 4000-byte boundary and all
accepted text bytes stay unchanged.

The helper drains the final usage frame before rejecting completed output, so
paid input/output/cache/reasoning usage remains recorded with `Canceled=false`
and `Failed=false` for a successful transport. The existing optional side-model
fallback still makes at most one active-model fallback call. No new retry or
semantic checking inference was added.

This guards structural failures and application-side truncation. A short,
normally completed summary can still omit a requirement. The change does not
claim automatic semantic verification of every model-produced summary, rollback
of earlier tool pruning, or restoration of facts lost in previously saved summaries.

## Rejected: move the catalog before project instructions

The prototype admitted only the concrete OpenAI Chat transport, including known
Metered wrappers. Signed Anthropic, Responses, Codex and unknown transports kept
their exact previous system order. Focused fixtures verified native continuation,
resume, instruction inventory, media, tool evidence, estimates and disabled modes.
The production file and three added policy/test files have been archived outside
application packages; `loop_complete.go` was restored byte-for-byte.

A portable diagnostic used production `NewLoop/Run`, the configured local
`qwen3.8-27b-uncensored`, and two independent read-only file operations per project.
The representative stable tool catalog was identical across projects. The model
had to retain the active workspace and both exact returned values, then stop.

Six serial diagnostic processes performed twelve successful project tasks:
24 model requests and 24 requested reads, with no duplicate reads, tool errors,
or dormant-catalog operations. The HTTP payload checks confirmed identical
non-message controls, tool contracts and first-task messages, and identical
system instruction segments apart from the intended ordering.

Controls were `reasoning_effort=high`, thinking enabled, configured `max_tokens=0`;
the last value means no diagnostic override. The real wire controls were compared
as well. Qwen was a test backend, not a production model-name condition.

Native prompt timings for the first request after changing projects:

| Case | Existing order | Prototype | Native evaluated tokens, before → after |
| --- | ---: | ---: | ---: |
| Initial, unseen project | 3162.05 ms | 833.27 ms | 3068 → 518 |
| Same projects repeated, reverse process order | 433.15 ms | 828.03 ms | 204 → 516 |
| New project identities | 3219.86 ms | 834.50 ms | 3080 → 518 |

The unseen-project median improves by 73.9%, but returning to a previously used
project regresses by about 395 ms. Native work shows reuse of more project context
with the old order in that repeated case; the observations do not isolate a
specific on-disk cache restore phase. This tradeoff does not justify replacing the
existing stable layout by default.

The native runtime PID was 15664 throughout. Four task IDs per diagnostic process
matched the HTTP request intervals and output usage. Prompt/generation/total
timings were observed for all 24 calls; one last `task_end` was not observed before
the diagnostic observer closed. The HTTP stream and matching totals completed.
This missing event is recorded instead of being treated as complete observer
coverage. Chat usage reported zero cached tokens even when native prompt work
was reused; cache savings use native evaluated-token counts, not that zero value.

Generation remained separate and variable. In the fresh-identity changed-project
comparison both outputs happened to contain 285 generated tokens including 105
reasoning tokens: generation took 7749.36 and 7755.52 ms. This is evidence of
reduced prompt work, not faster token generation. The prior compaction diagnostic
also found cached prompt processing could be short while reasoning generation
remained the dominant time; no shorter-thinking setting was introduced here.

## Validation and portable artifacts

Artifacts are in `.tmp/cache-prefix-followup-2026-10-11/` under the application
repository. The baseline behavioral tests reproduce all three targeted issues:
lossy configured-compaction fallback, silent summary truncation, and duplicate
goal queries. The summary baseline overlay adds only the new error declaration
needed to compile the new tests; its old acceptance/truncation implementation is
unchanged.

The first integration run exposed two old GUI success fixtures that used Echo
to return the full oversized transcript and depended on silent truncation. Those
fixtures were replaced with short structured summaries through the real
engine summarizer, preserving and strengthening their success assertions. Added
HTTP regressions verify the unchanged archive, previously saved projection and
new-loop resume after oversized output, transport failure and cancellation.

Final tests and `go vet -p 1` passed for `internal/llm`, `internal/llm/providers`,
`internal/llm/factory`, `internal/system/config`, `internal/agent`, `internal/webgui`
and `internal/app`. The CLI and GUI were built serially, their version/help paths
passed, and both executables were installed with verified recoverable backups.
The application version remains 1.0.5; the new executable code takes effect on
the next launch. Integration/build validation is recorded in the stage receipts.
No model
warmup, cache reset, model load, slot pinning, VM intervention, progress polling,
commit, push, or release was performed.
