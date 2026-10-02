# Small command-result echo reuse

Baseline: `306ff56`, dev13. This change omits a byte-identical 64–255-byte
command echo from the model view only when the complete original result fits
within 1 KiB. The paired assistant tool-call arguments retain the command.
Original result text, UI serialization, errors, diagnostics and retained output
remain unchanged. No prompt, tool schema, provider transport, Zen path, cache,
service or startup hook was added.

## Producer-local implementation

`CtxExecuteTool.Execute` marks the JSON it has just marshaled from the typed
`ctxexec.Result`. The new helper accepts that private fresh-result marker; it is
never called on arbitrary custom tool text, persisted history or provider JSON.
The existing long-command and HTML-heavy projections run first and retain their
exact previous behavior.

For a newly eligible result, argv equality is checked without allocating a
joined copy. A bounded scan locates the own serializer's unique string command
member. Escaped occurrences inside stdout/stderr cannot match the structural
marker. Missing, ambiguous or unexpected shapes return the original view. All
bytes outside that single member are copied unchanged, including additional
members; no output stream is decoded or encoded again.

The new model string still requires one allocation. Its source JSON is limited
to 1 KiB. This is equivalent input reuse, not a zero-copy or retained-RAM claim.
Full original `Result.Text` and serialized UI evidence are byte-identical.
OutputStore needs no new save/read/handle for these inline views. As with the
existing long-echo path, the chosen equivalent model text is stored in tool
message history together with its original paired call.

## Bounded saved incidence

Six previously selected completed local sessions were reused through their
existing in-memory audit metadata. No raw prompts, command bodies, project
paths, session IDs or credentials were exported. There were 1,041 paired
`ctx_execute` results, 816 structured inline successful results and 810 exact
command echoes. The existing long-command path already covers 235 records.

The remaining moderate-command candidate has 324 records across those sessions.
Limiting original JSON to 1,024 bytes keeps 246 records and 38,983 removable
model-text bytes: 73.4% of the unbounded candidate's 53,085 bytes. These are
aggregate result bytes, not token counts or reductions in whole requests.
There is no historical claim about unnecessary turns or repeated execution.

The mechanism is consistent with the local Pi bash tool, whose result contains
output, truncation/reference, exit code and duration while the paired call
retains the command (`pi-main/packages/coding-agent/src/core/tools/bash.ts`,
lines 394–411). Executed commands that differ from submitted argv are preserved.

## Cost measurement

The isolated R11 complete-pipeline experiment included the original typed JSON
marshal and compared the rejected R10 extra-re-encode adapter with this fresh
producer view. For a 575-byte small result, medians were:

| Complete pipeline | ns/op | Transient B/op | Allocations |
| --- | ---: | ---: | ---: |
| Existing baseline | 1,403 | 1,152 | 2 |
| Previous extra-re-encode adapter | 3,249 | 2,769 | 7 |
| Fresh producer view | 1,703 | 1,601 | 3 |

Those measurements used three 200 ms samples on Windows amd64, Ryzen 7 5800X3D,
CPU=4. The larger 4 KiB-result fixture still needed an expensive model string
copy; the accepted 1 KiB cap declines that case entirely.

The durable helper-only benchmark after the production port measured:

| Helper fixture | Median ns/op | Transient B/op | Allocations |
| --- | ---: | ---: | ---: |
| 63-byte command, unchanged | 7.686 | 0 | 0 |
| 128-byte command, small result | 312.9 | 384 | 1 |
| Exact 1,024-byte original result | 410.5 | 896 | 1 |
| 1,025-byte original result, unchanged | 6.162 | 0 | 0 |
| Long-command legacy guard | 7.508 | 0 | 0 |

The helper-only table is an incremental helper cost, not the total tool cost.
Allocation bytes are transient measurements, not application memory residency.
Plain messages with no tool call do not reach this result serializer.

## Correctness and offline wire fixtures

Scoped workflow/ctxexec tests and vet passed. New tests cover:

- Command boundaries 63/64/255/256 bytes and exact original JSON sizes
  1,024/1,025 bytes; full UI bytes and all result members except the exact echo.
- stdout/stderr, exit code, workdir, duration, truncation, warnings and incomplete
  capture; quotes, backslashes, UTF-8, HTML escapes, invalid UTF-8 encoding and
  quoted fake member markers; additional typed members copied intact.
- Nonzero exit, runner/tool error, cancellation cause, retention, previews,
  existing ModelText, changed/wrapped/quoted command, missing argv and nil
  result. Existing long/HTML behavior and foreign custom result text remain
  unchanged.
- Public registry execution of an owned test helper, including native and
  JSON-encoded argv. No project/user command is run by these fixtures.
- Actual OpenAI serialization through a fake in-memory HTTP transport for
  native `ctx_execute` and `invoke_tool` pairs. The before/after request bytes
  differ only in the exact tool-result string; arguments, message order, tool
  IDs, empty schema set and other request fields are identical.

The opt-in synthetic request fixture export is restricted to the repository's
portable `.tmp`. It uses an invented command, a synthetic workdir and result;
no user data or API key is present. Both protocol pairs remove 141 model JSON
bytes / 145 escaped request bytes in this fixture. Requests contain one user
message, one assistant tool call and one tool result, with no enabled tools.

Files under `.tmp/goal-harness-round11-2026-10-03/wire-fixtures`:

- `ctx_execute.before.json` and `ctx_execute.after.json`
- `invoke_tool.before.json` and `invoke_tool.after.json`
- `METADATA.json`, including exact SHA-256 hashes and topology

The model field is the placeholder `ctx-short-fixture`. These are offline
serializer fixtures. The root's single read-only LM Studio model-list request
found no available connection, so no native POST ran in this round. Native
input-token delta, prefill effect, decoding speed and answer equivalence are
unmeasured. Byte savings alone must not be presented as faster model generation,
fewer turns or less RAM.

Portable reproduction and completed tests/bench/vet outputs are under
`.tmp/goal-harness-round11-2026-10-03`; source hashes identify the final port.
