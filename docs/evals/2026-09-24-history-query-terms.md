# History queries with filenames and paths — 2026-09-24

## Problem and change

A persisted tool result from the previous controlled recent-evidence experiment contains an SQLite FTS5 syntax error near a dot for the query `RetryDelay MaxAttempts retry.go`. Unquoted punctuation can turn a reasonable history lookup into an error and another model round.

`search_history` now quotes punctuation-bearing bare terms before its existing database query. For example, `BudgetLimit src/retry-policy.go` becomes `BudgetLimit "src/retry-policy.go"`. This is one local lexical pass, with no retry, second database query, helper inference, new instruction or schema growth. Session, role and time filters, ranking, result limits, archive content and snippets are unchanged. Both local and cloud providers use the same tool. The special OpenCode Zen transport is untouched.

Existing quoted strings, escaped quotes and FTS operators retain their semantics: AND, OR, NOT, NEAR, prefix matches, column filters and anchors. Windows drive paths and UNC paths are handled. Malformed explicit expressions remain errors; the implementation does not drop terms or substitute OR for AND. Syntax rules were checked against the primary [SQLite FTS5 query documentation](https://www.sqlite.org/fts5.html#full_text_query_syntax).

## Controlled model comparison

The reproducible harness is `scripts/history-query-terms/main.go`. Two builds differ only in the call-site use of the new helper; the baseline uses a Go overlay restoring the previous call site. Each has its own portable SQLite history store with synthetic archived tool evidence containing `src/retry-policy.go` and `BudgetLimit=6842`. This is seeded evidence, not a previously executed file read.

The harness executes a fixed initial history query and passes its actual result or error into the agent loop. It then measures the model's continuation until the answer. Only history/output retrieval tools are exposed, with no file mutation or shell tools. The quoted control starts with a valid explicit phrase. Each model/case has one before/after pair; these are eight runs, not a statistical performance study or an unconstrained real project task.

| Model / query | Model calls before → after | Extra retrieval tools | Seconds before → after | Input tokens before → after |
| --- | --- | --- | --- | --- |
| Qwen, bare filename/path | 3 → 1 | 2 → 0 | 9.719 → 2.587 | 3248 → 975 |
| Muse, bare filename/path | 2 → 1 | 1 → 0 | 8.390 → 4.152 | 2807 → 1278 |
| Qwen, quoted control | 1 → 1 | 0 → 0 | 3.863 → 3.035 | 981 → 986 |
| Muse, quoted control | 1 → 1 | 0 → 0 | 5.812 → 3.507 | 1286 → 1286 |

All eight answers gave the correct value and file. The baseline Qwen first retried with another punctuation-bearing query and then used a simpler query; Muse needed one retry. After normalization, both answered without another tool. The initial error payload was 107 bytes; the successful evidence was 132 bytes. The quoted control stayed at 132 bytes.

Models: `qwen3.8-27b-uncensored` through LM Studio and `muse-spark-1.3-contributor-free` through Zen, reasoning low. The first pair ran Qwen baseline and Muse candidate; the second reversed variants. Different providers ran concurrently, while each provider's cases were sequential. Timings include model continuation and its tools, not the fixed initial lookup. Provider load, caching and generation variability affect wall time; input totals include cached tokens and are not cost estimates.

## Local preprocessing cost

On Windows / Ryzen 7 5800X3D, three samples of 100,000 iterations each:

| Query shape | ns/op samples | Allocated bytes/op | Allocations/op |
| --- | --- | --- | --- |
| Ordinary words | 38.58 / 40.12 / 39.87 | 0 | 0 |
| Bare filename/path | 136.6 / 124.5 / 125.2 | 112 | 3 |
| Already valid advanced expression | 79.27 / 74.38 / 74.87 | 0 | 0 |

This measures the helper alone, not SQLite, full agent latency or provider billing. The common unchanged path returns the original string without allocation.

## Validation and limits

- The real-store regression failed before the production change with the dot syntax error and passed afterward.
- Tests cover filenames, relative/Windows/UNC paths, quoted strings, escaped quotes, boolean expressions, NEAR, prefixes, columns, anchors, Unicode and idempotence.
- Real database checks prove that terms in different records do not accidentally satisfy AND, that NOT still excludes, and malformed or unknown-column expressions still fail.
- `go test -timeout=90s ./...` and `go vet ./...` passed.

This fixes syntax, not relevance or index coverage. A filename present only in unindexed call arguments will still not match the message body; the original observed multi-term query is therefore not guaranteed to find a record in one call. Filenames containing structural FTS operators, spaces or a leading minus still need explicit quoting. FTS tokenization and phrase matching remain in effect; this is not literal substring search. No universal speedup or elimination of all repeated searches is claimed.

Local artifacts are under `.tmp/history-query-terms/`: baseline overlay, failing/passing regression outputs, model runs, comparison, benchmark and full checks. Release build, smoke and installation verification results are saved there as well.
