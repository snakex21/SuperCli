# Match build/test forms during tool discovery

## Evidence

The live continuation replay's query "run go test build check" returned ctx_execute, edit_docx and list_dir. The latter two matched incidental words ("run" in Word text runs and "build" in directory exclusions). The relevant command description says "builds/tests", which the fallback tokenizer treated as unrelated to the singular query. Its equal weak matches consequently survived the existing default-focus filter.

The current-code regression uses the real command, Word and directory tool specifications through the public search tool, with both an in-memory index and no index. Six assertions fail before the fix, covering the recorded query and complementary-intent queries.

## Change

The fallback token visitor folds only tests -> test and builds -> build, identically in query words, tool names/descriptions and focus coverage. Other vocabulary stays literal. The existing focus filter can then keep the stronger command match and drop weaker results covering no additional query intent.

Exact registered tool names still win before tokenization. Explicit result limits preserve breadth; broad "run" queries preserve tied alternatives. Queries also mentioning Word or a calendar retain those distinct capabilities. Schemas, activation and the special Zen path are unchanged. No new instructions, model calls, provider-specific behavior or external dependency.

## Measurement

For the recorded query in the real-tool fixture:

| Metric | Before | After |
|---|---:|---:|
| Result bytes | 8,016 | 1,706 |
| Local estimated result tokens | 2,405 | 535 |
| Search time | 201.8–205.4 microseconds | 58.1–59.6 microseconds |
| Transient allocations | about 91.4 KB / 870 | about 21.3 KB / 183 |

Three 300 ms benchmark samples, Windows / Go 1.26.2 / Ryzen 7 5800X3D. The result is smaller because unrelated schemas are not emitted or activated, not because the correct schema is truncated. Token figures use the local estimator and are not provider billing measurements. No live reduction in repeated model turns is claimed.

Default-focus, lexical-ranking, activation, exact-name and concurrent-query tests pass. Full go test -timeout=90s ./..., go vet ./... and both application builds pass. The optional live test remains opt-in.

Artifacts: .tmp/tool-discovery-command-forms-2026-09-27/ contains the original searcher, red/green regressions, benchmarks, complete checks and installation records. The preceding live workflow is documented in 2026-09-27-worker-continuation-replay.md.
