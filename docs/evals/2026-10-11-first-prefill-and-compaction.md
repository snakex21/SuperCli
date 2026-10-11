# First project preparation and compaction: measured boundaries

## Production compactor, current local profile

Two sequential calls use the unmodified production `SummarizeForCompaction` on the older prefix of an already completed **synthetic** coding fixture. Historical commands are not run again. The fixture's projected transcript (25,996 bytes) and instruction (870 bytes) are checked byte-for-byte before any inference. No tools are offered. A passive HTTP/SSE observer and the existing numeric native-log observer collect completed-operation evidence; there is no progress polling.

Both actual HTTP bodies are identical: 27,483 bytes, SHA256 `e8c0618f96f47b194baa020318b1a8de1b65d7ee7dccc5562359613d526c0b60`. The active portable project configuration resolves `high`, thinking enabled, no request output-token override, and `cache_prompt:true`. Those controls remain unchanged. The historical `xhigh` experiment is **not** an A/B baseline for these new calls. No model loading/unloading, cache eviction, slot pinning, context/checkpoint changes, VM changes, or warm-up requests occur.

| Boundary | First call | Identical repeat |
| --- | ---: | ---: |
| Native task / runtime PID | 896 / 15664 | 1585 / 15664 |
| Slot selection | LRU | LCP, similarity 1 |
| Selection → task launch | 166.762 ms | 0.252 ms |
| Native prompt evaluation | 8,138.90 ms / 8,180 tokens | 132.06 ms / 4 tokens |
| Native generation | 19,073.01 ms / 682 tokens | 42,487.51 ms / 1,503 tokens |
| Reported reasoning tokens, included in output | 265 | 1,291 |
| Client first model output | 8,400.217 ms | 153.935 ms |
| Client first visible summary text | 15,865.376 ms | 36,683.230 ms |
| Client whole helper | 27,491.248 ms | 42,644.142 ms |
| Separate native metadata discovery | 15.181 ms | 11.052 ms |

The exact repeat substantially reduces actual input evaluation, while its total duration increases by 55.12% because generation is larger. An unchanged effort setting does not imply an unchanged number of reasoning tokens. The earlier 116-second selection/preparation stall is not reproduced: the first new selection-to-launch interval is 0.167 seconds. These results identify the current costs; they do not establish a code speedup, a cold-model benchmark, or a fixed latency guarantee under a different VM/memory load.

The observer receives one unambiguous native task per call; runtime task IDs, monotonic clock differences, matching generation counts, and client time windows support correlation. Native `prompt_eval` counts are evaluated tokens, not complete request-token counts or exact cache-hit counts. Raw SSE contains no timing/cache-token fields here; the default zero in the client usage must not be presented as proof of zero cache usage. The logger initialization banner is not a subscription acknowledgement. Exact queue, save, load and allocation durations remain unknown.

## Summary quality

The first summary preserves the invoice fix, cause, reproduction/result, pending `docs/API.md`, quantity contract and ongoing constraints, including explicit prohibitions on changing tests/catalog, dependencies and publishing. Its quote is abbreviated and one failed expected numeric zero is described as a quoted string, so literal-perfect reproduction is not claimed.

The repeat preserves the main task state but loses the future prohibition on changing tests/catalog: it describes past compliance instead. It also omits the workspace, protected paths and test command. It is **not a complete quality pass**. No experimental prompt rewrite, truncation, reduced thinking, or shorter model output limit is adopted from these measurements. These diagnostic helper calls do not modify a real conversation or replace its stored history. A later end-to-end continuation was not tested.

## First-turn preparation audit

Current preflight performs fresh Git status and the already coalesced static log concurrently, with a 300-token briefing budget. The previous complete cold collector measurement is about 49 ms; it is not the multi-second model processing interval. The compaction renderer already visits each message once and omits the tool catalog. Further reuse of pure history projections would save small local allocations, but its current tiny-fixture boundary is only 12–17 µs and does not justify a broader cache with stale archive/permission gates.

The GUI's `context_prepare` phase starts after model metadata discovery, loop/memory preparation and initial preflight. It must not be treated as the entire submit-to-HTTP interval. The metadata-only changes below target redundant preparation reads; they do not shorten the model's reasoning or change the context sent for coding or compaction.

## Accepted change: overlapping native metadata reads

`ListLocalNativeModelInfos` now shares the native HTTP read only while calls overlap for the same normalized API root and credentials. The pending-read key includes the full SHA256 of the cleaned credential; this does not change the existing runtime-snapshot key. No completed result or error is kept by the new mechanism, so a later independent call performs fresh discovery. The change is generic to the supported local/private native discovery endpoints and does not branch on a model name.

Each caller keeps its own cancellation/deadline and receives an owned result. A canceled starter does not cancel a healthy follower; canceling the last waiter cancels the abandoned transport. Explicit invalidation or a newer runtime snapshot prevents a fresh caller from joining the older read, and an older response cannot restore an invalidated context limit. The original native endpoint fallback, discovery timeouts and independent runtime-context refresh behavior remain intact. Pending entries are bounded at 64; overflow uses the existing independent read behavior.

Five serial, rotated before/after pairs use the unchanged public API and a gated loopback HTTP server. The baseline production file is frozen before the change. Each response is parsed and compared with the complete expected `ModelInfo`; the next non-overlapping call is checked for fresh HTTP discovery. This is a metadata benchmark with a 246-byte fixture, not a model inference or resident-memory benchmark.

| Overlapping callers | Native GETs before → after | Median allocated bytes/group before → after | Median allocations/group before → after | Median elapsed time/group before → after |
| --- | ---: | ---: | ---: | ---: |
| 1 | 1 → 1 | 13,967 → 15,071 | 195 → 205 | 71.041 → 72.320 µs |
| 4 | 4 → 1 | 82,831 → 17,712 | 930 → 237 | 5,071.495 → 84.828 µs |
| 8 | 8 → 1 | 192,404 → 21,233 | 1,996 → 278 | 16,762.119 → 93.924 µs |

The reliable benefit is 75%/87.5% fewer native GETs for four/eight overlapping callers and fewer allocations. Loopback elapsed times include both client and synthetic server, HTTP pooling, TCP and scheduling, and vary substantially; they must not be extrapolated to whole-agent or compaction latency. A single caller has no measured CPU gain: median overhead is 1.279 µs, 1,104 bytes and 10 allocations. This avoids duplicate work where discovery overlaps rather than claiming faster first prompt evaluation.

## Rejected change and validation

A separate GUI proposal skipped worker metadata when delegation was disabled. Independent review found a settings-change race: delegation could be enabled while the main-model refresh was in flight, leaving the newly created worker without a fresh context snapshot. The proposal and its tests are archived outside production packages; `engine_context_refresh.go` is restored byte-for-byte. No GUI refresh suppression is included.

Focused discovery and runtime-freshness tests pass, including cancellation, independent deadlines, endpoint/credential separation, negative-result freshness, native fallback, snapshot invalidation, bounded overflow and reentrant context hooks. A public-API overlap test fails against the frozen baseline with eight GETs and passes against the candidate with one GET. The focused race-enabled run passes. An independent read-only review checks the implementation and all 30 benchmark rows against the ten completed logs.

Full tests and `go vet` pass for `internal/llm`, `internal/llm/providers`, `internal/llm/factory`, `internal/system/config`, `internal/agent`, `internal/webgui` and `internal/app`; `git diff --check` passes. The previously validated 35 Go source files remain unchanged. The build verifies the 40-file candidate source manifest, builds native CLI and GUI serially, and checks CLI `--version` and GUI `--help`. Both portable executables are installed with verified backups; version remains 1.0.5. No commit or release is created by this follow-up.

## Evidence

Portable artifacts: `.tmp/project-first-prefill-2026-10-11/`. `live-first.json`, `live-repeat.json`, both `native-*/runtime-metrics.jsonl`, observer receipts and `completed-comparison.json` hold the completed measurements. `live-compact.go` checks original instruction/transcript bytes and blocks every second HTTP request, including compatibility retries. `numeric-sse.go` is the existing bounded passive observer, copied without parser changes. `analyze-completed.ps1` checks IDs, counters, request equality and controls after completion. An independent read-only review reconstructs the transcript and instruction and confirms the timing and quality limitations above.

`discovery-comparison.json`, `discovery-*-baseline.log`, `discovery-*-candidate.log`, `baseline-public-red.log`, `candidate-public-green.log`, `race-focused.log`, `independent-discovery-review.md`, `verification.json` and `candidate-sources.json` preserve the source/contract and benchmark evidence. `build-validation.json` and `installed.json` record the completed native build and installation. Prototypes rejected during review are under `rejected/` and are excluded from the package builds.
