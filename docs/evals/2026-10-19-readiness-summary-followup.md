# Readiness retention and short-summary follow-up

This follows `2026-10-19-short-summary-ab.md`. The shared compaction and pruning
code now distinguishes an unrecorded readiness check from unknown metadata and
from a previously verified check. It does not change model selection, reasoning
effort, sampling settings or API output budgets. After the corrected retention
series, the shorter state prompt was promoted to the shared auto-summarizer.
The standalone summarizer keeps its self-contained 500-token prompt.

## Confirmed defect and correction

A generated `process_session` failure can contain the process ID and exit code
without `ready` or `ready_at`. Previously, retaining the latest status replaced
the whole process fact, silently losing an earlier verification timestamp.
A failing regression reproduced this before the production correction.

The latest status/exit remain authoritative. An earlier valid timestamp now
survives as `past_ready_observed_at`, with `readiness_history=observed`; the old
current-ready flag never survives a partial failure. New process launches clear
history for a reused ID, including after an application restart. Process IDs
from different processes are never merged.

Structured metadata additionally records `readiness_history=not_recorded` when
`ready=false` has no recorded timestamp, and `unknown` for missing/inconsistent
metadata. A null or invalid timestamp is not evidence that verification never
happened. A valid timestamp in an older record without `ready` remains historical
evidence. No evidence is extracted from arbitrary log text or assistant promises.

For new summaries with an exact instruction carry, previous process facts are
read only from the host suffix after that carry. A lookalike line in model prose
cannot become verification evidence. Older summaries without that carry retain
their legacy compatibility path.

Regression cases cover generated partial errors and JSON snapshots, full and
pruned results, 20 consecutive host-fact carries per case, new launches with
reused IDs, unknown/null/invalid metadata, stopped processes with stale ready
flags, foreign-process/log text, and forged model-prose observations.

## Investigating the original live discrepancy

The earlier failed smoke recorded only the field `readiness_ever_verified`, not
the summary or actual answer. It cannot establish whether the original failure
was in summarization or in the probe model's interpretation.

Before the new correction, a fresh short summary and three probes of that exact
same input all recovered the tested state correctly. Request input hashes were
identical across probes. This did not reproduce the earlier error and does not
prove that the earlier error was exclusively model behavior. The independent
code defect above was reproducible and was fixed regardless.

## Live-test methodology and fixture correction

Tests use the copied long-session fixture with the existing Codex provider,
`gpt-6.1-sol` and unchanged `xhigh`. The original session databases and OAuth login
are not rewritten or refreshed. Normal tests skip live calls without explicit
opt-in. All temporary data and builds stay in the portable repository stage.

The previous five-round case is extended with a sixth-round partial process
failure after historical readiness was verified. Each round checks 11 state
fields, exact user/legacy carry equality, and SQLite projection save,
close/reopen and exact readback. Visible candidate length is a soft 200-token
target; necessary state may exceed it. No API generation cap is reduced.

The first extended run used an invalid fixture action, `status_summary`, and an
ID that did not match the returned process ID. Both formats passed rounds 1–5
and retained the readiness fields in round 6, but the baseline probe returned
`command_failed` as the unresolved error, and the short probe chose the older
exit 0 instead of the host-retained exit 9. These failed results remain in
`.tmp/readiness-summary-2026-10-19`; they are not counted as passing tests.

The corrected fixture uses the real `poll` action and matching process ID.
The oracle now explicitly separates application errors E42/R17 from process
exit codes. A normal offline test validates fixture actions against the actual
`process_session` tool schema. The corrected run uses a fresh stage,
`.tmp/readiness-summary-v2-2026-10-19`, rather than replacing failed artifacts.

## Corrected live results

The corrected run passed **both variants in all six rounds**: 132 state-field
comparisons, 12 exact user/legacy carry checks and 12 SQLite close/reopen checks.
The sixth round recovered exit 9, historical verification at
`2026-10-19T10:30:00Z`, and no currently verified readiness. No tested changed
value, constraint, worker recovery, cancellation, application error or pending
task was lost.

Three paired first-compaction trials used the same transcript hash, alternating
baseline/short order (AB, BA, AB):

| Trial | Existing prompt | Short prompt |
|---|---:|---:|
| 1 | 20.44 s | 19.68 s |
| 2 | 21.95 s | 23.10 s |
| 3 | 24.94 s | 19.98 s |
| Mean | 22.44 s | 20.92 s |

The measured mean helper time fell **6.8%**, with one slower pair. Mean visible
output fell from 516.7 to 293.7 tokens (43.2%); mean visible summary bytes,
including fixed resume framing, fell from 2677.7 to 1646.3. The protected
instruction carry is separate and is not clipped.

The five later compactions took 194.48 seconds in the baseline chain and 76.56
seconds in the short chain. Those chains ran sequentially with different
preceding summaries, so this is descriptive, not a randomized speed claim.
Cloud scheduling, prompt-cache reuse and generated reasoning vary. The user
effort was verified as `xhigh` on the actual requests, not inferred from visible
text length. No throughput (tokens/second) improvement is claimed.

## Production promotion and scope

Only the shared `NewAutoSummarizer*` path uses the shorter state instruction.
It always appends the exact instruction carry and bounded owned tool/process/
worker facts. The public standalone `SummarizeForCompaction` function still
requests a self-contained summary because its callers do not necessarily append
that protected carry. The existing completion, empty-template echo and 4000-byte
rejection guards remain; long necessary state is never forcibly truncated to
200 tokens. Side-provider fallback uses the same selected state instruction.

The evaluated candidate text is frozen in the test and compared byte-for-byte
against the production instruction. Chat, Responses and Anthropic request-builder
tests check that only the prompt changes; reasoning, sampling and token-budget
fields do not. The code has no model-name/provider branch for the optimization.
Live factual and timing measurements were made on Codex only, not on every model.

The direct live production smoke, without a prompt-swapping test wrapper,
passed after promotion: 22.50 seconds, unchanged `xhigh`, all first-round state
fields and exact instruction carry intact. Its wire instruction hash matched the
evaluated candidate exactly.

## Validation and files

After promotion, the following completed successfully:

```
go test ./internal/agent/... ./internal/llm ./internal/storage/session ./internal/storage/memory ./internal/tools/processsession/... -count=1
go vet ./internal/agent/... ./internal/llm ./internal/storage/session ./internal/storage/memory ./internal/tools/processsession/...
go build ./...
go test ./internal/app ./internal/webgui -count=1
go vet ./internal/app ./internal/webgui
```

The frontend tests passed as well (app 5.90 s, web GUI 126.76 s).
Targeted compaction failure/echo/oversize, resolver, provider-wire and readiness
regressions also passed. Fresh CLI and GUI builds passed `--version` and hidden
`-h` checks respectively. The original running/main executables were not
overwritten. Builds are under `.tmp/readiness-summary-v2-2026-10-19/`:
`supercli-readiness.exe` and `supercli-web-readiness.exe`.

Production changes are in `context_summarize.go`, `context_prune_process.go`,
`context_process_facts.go`, and `context_file_facts.go` under `internal/agent`.
Readiness regressions live in `context_process_readiness_test.go`; the short
summary experiment, wire/production-contract, and fixture-contract tests are
`summary_short_experiment_test.go`, `summary_short_wire_test.go`, and
`summary_short_fixture_test.go`. Existing auto-summary resolver, failure,
oversize, echo and preparation tests were updated to check the selected short
instruction while preserving standalone and archived-receipt contracts.
