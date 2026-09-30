# Request preparation without rescanning completed replies

Baseline: source commit [5c5fea6](https://github.com/snakex21/SuperCli/commit/5c5fea6023f2815e13bb1f7ce2d4861db029556f).

The loop formerly priced the full assembled request again after its prune and compaction estimates. Unchanged coordinator history now reuses the existing append-only token estimate and prices only request-specific changes: merged leading systems/catalog and the trailing context. Hidden history, media, removed native reasoning and omitted tool exchanges keep full wire-view estimation. No new history cache or invalidation rules were introduced.

When previous reasoning is omitted, finding the last completed reply formerly parsed every older answer from the beginning. Searching backwards stops at the latest eligible reply. The same native state is removed or retained, with tool-call reasoning and unfinished tails still protected.

## Validation

The request parity test covers coordinator, chat, advisor and clarify routes in native/thin tool modes, with plain and multiple leading systems, hidden history, native state, omitted reasoning, active/dormant images, resolved tool evidence, explicitly retained reasoning and final-only replies. It checks cold/warm requests, appends and edited history. The local estimate must equal the unchanged estimator applied to the actual assembled wire payload. Archive JSON remains identical. Explicit boundary tests cover the latest visible reply, an unfinished tail, no completed reply and no later user turn.

Full repository validation passed: `go test ./... -count=1 -timeout=120s` and `go vet ./...`.

## Measurements

Synthetic long conversation: 40 historical code replies, plus user turns and a final continuation. No network, request serialization or model inference is included. The sequence includes two threshold estimates, tool-definition construction, message assembly and final request pricing. Before/after binaries were run alternately twice; the table uses medians of four samples per case with one Go CPU and 300 ms benchmark intervals. The baseline overlay contains the original two production files plus a benchmark-only adapter for the old complete-request scan.

| Tool exposure / history | Before (microseconds) | After (microseconds) | Speed ratio |
| --- | ---: | ---: | ---: |
| Native / text | 60.495 | 32.419 | 1.87x |
| Thin / text | 67.063 | 22.194 | 3.02x |
| Native / omit old reasoning | 2126.394 | 182.616 | 11.64x |
| Thin / omit old reasoning | 1943.896 | 160.234 | 12.13x |
| Native / hidden prefix | 58.704 | 62.082 | 0.95x |
| Thin / hidden prefix | 44.399 | 44.465 | 1.00x |
| Native / previous image | 71.127 | 62.929 | 1.13x |
| Thin / previous image | 54.968 | 51.135 | 1.07x |

With omitted reasoning, temporary allocation fell from about 1.2 MB to 98–104 KB per sequence. Hidden/media cases retain full estimation; the small timing differences are not evidence of a universal speedup. The native hidden fixture measured about 3.4 microseconds slower, with identical allocation volume. This is a preparation improvement, not an 11–12x end-to-end model speedup. Prompt bytes, model token cost and provider routing are unchanged.

Run the current benchmark:

```sh
go test ./internal/agent -run '^$' -bench '^BenchmarkPreparedRequestSequence$' -benchmem -benchtime=300ms -count=2 -cpu=1
```
