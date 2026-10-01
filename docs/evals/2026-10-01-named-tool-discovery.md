# Explicit tool list discovery

Baseline: `06dee973780d7454f2fceae980ec332a166fb8dc`. This is a deterministic orchestration check with synthetic local source files, not live inference or a latency/cost benchmark.

## Problem and change

A query such as `read_many, search_code, read_context` previously went through lexical matching. On a registry using real tool descriptions, it returned `ctx_execute`, `edit_docx`, and `read_context`: two requested names were omitted and irrelevant schemas were activated. Indexed and non-indexed discovery both reproduced this outcome. `search_code and patch_file` similarly omitted `patch_file`.

Queries made entirely of registered tool names now resolve those names in request order. Commas, semicolons, pipes, ampersands, whitespace and the conjunction `and` can separate names. Duplicate names are returned once. The existing default/requested/hard limits apply. Unknown words, names absent from the caller's registry and ordinary intent queries retain the existing search path. Exact-case names win. Ambiguous case-insensitive list names retain the existing search fallback instead of guessing an identity. Single exact-name lookup remains unchanged. Discovery does not execute a tool or alter its argument validation and permissions.

## End-to-end replay

`TestNamedDiscoveryAvoidsRepairRound` uses a deterministic provider that requests the three tools, repairs missing discoveries only when needed, then uses the real read/search/context tools on two temporary files. It checks the resulting evidence before producing its answer. The baseline fails the new no-repair assertion while still producing the same correct fixture answer.

Observed one-run measurements:

| Measurement | Baseline | Updated |
|---|---:|---:|
| Provider requests, either schema mode | 4 | 3 |
| Discovery tool calls | 3 | 1 |
| Total tool calls | 6 | 4 |
| Full discovery-result bytes | 9,530 | 1,682 |
| Cumulative serialized request bytes, native | 70,076 | 13,150 |
| Cumulative serialized request bytes, thin stable tools | 71,607 | 14,350 |
| Fixture evidence complete | yes | yes |

Request byte totals can vary slightly with temporary-path or request-clock text. These are JSON byte counts, not provider-reported tokens or prices. The replay proves that this particular list no longer forces a discovery repair round; it does not establish how frequently real models use such lists or how much faster a live task will finish.

Focused reproduction:

```sh
go test ./internal/tools/search ./internal/agent -run 'TestDiscoveryExactNameLists|TestExactNameList|TestNamedDiscoveryAvoidsRepairRound' -count=1 -v
```

Additional guards cover indexed and non-indexed search, caller order, case handling, duplicate names, explicit/default/hard limits, unknown and natural-language queries, meta-tool exclusion, activation scope and exact schema preservation. Existing search/agent tests remain part of validation. No paid API, external model, credential, user transcript or user repository content is needed.
