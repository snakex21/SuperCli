# Explicit goal task additions

A completed session on 2026-10-07 issued three independent `goal` calls with
`action:"add_task"` and one `title` each (message sequences 12, 14 and 16).
No work or user message occurred between them. The task inserts took 2 ms;
the next assistant messages arrived after 3140 ms and 5120 ms respectively.
These are transcript intervals, not guaranteed inference savings. A model can
already emit multiple calls in one response, but this session did not do so.

`add_task` now also accepts an explicit `titles` list of 1–16 nonblank strings:

```json
{"action":"add_task","titles":["Inspect","Implement","Verify"]}
```

The original single `title` and its response stay compatible. Supplying both
non-null choices is rejected before writing. An unused `title:null` or
`titles:null` is treated as absent, including local registry validation; an
empty list or a missing valid choice is an error. Invalid list types and a
blank title anywhere in the list cannot leave earlier tasks behind.

The batch preserves supplied titles and order, allocates consecutive task
numbers, and adds pending tasks with one SQLite transaction. It reserves the
writer before checking scope, active status and the next number, invalidates
verification once, and refreshes the active service snapshot once after commit.
Failed inserts, verification updates, deferred commit constraints and caller
cancellation roll back every task and verification change. A failed rollback
discards the connection. Explicit lists do not call the heuristic or model
decomposer, and the change adds no persistent prompt instructions or new tool.

Regression fixtures cover ordering alongside existing tasks and concurrent
batches, the 16-item bound,
duplicate titles, preserved whitespace, project/global scope, inactive and
missing goals, validation failures, canceled live inserts, SQL failures at each
write stage, snapshot consistency, and nullable choices through both direct
execution and the registry. The existing verification test still requires both
missing arguments to be reported together.

`BenchmarkGoalTool_AddThreeTasks` compares three original single calls against
one batch through the real registry and SQLite service. Both routes create the
same three pending tasks from an empty active goal; row reset is outside timing.
It reports time, allocations and the exact number of tool calls. Central Go
tests passed for `internal/storage/goal` and `internal/tools/workflow`, including
the rollback and registry fixtures. Three 300 ms benchmark samples per route
gave these medians on the local Windows host:

| Same three tasks | Time | Bytes/op | Allocations/op | Tool calls |
| --- | ---: | ---: | ---: | ---: |
| Three single additions | 2.218 ms | 33,646 | 822 | 3 |
| One explicit batch | 0.739 ms | 12,723 | 334 | 1 |

These measure registry/service/SQLite work, without a model or network. The
batch enables a model to avoid intermediate calls; it does not guarantee that
every model will choose it or that the observed transcript intervals disappear.

The dormant tool's description is unchanged at 1680 UTF-8 bytes. Relative to
the pre-batch schema (including the earlier verification fix), the raw schema
grows from 1265 to 1554 bytes (+289); minified JSON grows from 1090 to 1358 bytes
(+268). A rough bytes/4 estimate is about 67 extra tokens for the minified
schema, not a model-tokenizer measurement. This cost is incurred when the tool
schema is discovered/activated, without enlarging persistent instructions.

A live contract smoke test used the publicly listed free Kilo model
`inclusionai/ling-3.1-flash`. With the complete 1680-byte production description
and 1358-byte minified schema, a synthetic Polish request to add three tasks
returned HTTP 200 and one `goal` call with `action:add_task` and the correct
three-entry `titles` array. The test did not ask explicitly for batching and
created no real project goal. The provider reported 1045 input / 174 output
tokens and zero cost; response wall time was 2.910 seconds. This tests schema
acceptance and model selection, not complete SuperCli/provider roundtrip speed.

An initial schema-only probe also returned the correct batch, but its harness
had accidentally omitted the description. It was retained separately and the
test corrected rather than treating it as production-description validation.
Both sanitized receipts contain only synthetic task data in the ignored local
evaluation folder. Neither test added instructions to the application prompt.
