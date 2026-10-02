# Versioned tool-definition snapshots

Date: 2026-10-02. Baseline: main 3eae0a5; Windows amd64, Go 1.26.2, Ryzen 7 5800X3D, GOMAXPROCS=2. Offline fixtures only; no GPU, Qwen, provider, network or live user sessions.

## Problem and change

The real request-preparation sequence estimates the next request for pruning and compaction, then assembles the provider request. Each estimate rebuilt the visible tool list, copying full Tool structs (including execution hooks), allocated another ToolDef slice, and rescanned unchanged descriptions/schemas. The earlier thin catalog hoist and direct definition assembly remain in place. This change caches only the latest derived definition set and its exact existing local token cost.

Registry.Revision changes after actual registration, always-on or activation/discovery changes. Failed registration, missing activation/deactivation, repeated calls and normal execution/output bookkeeping do not change it. All mutations of tools/order/visible/alwaysOn/discovered were inspected, including EnsureReadOutput, RegisterFrom and ActivateDiscovered. Descriptors store immutable strings by value; private compiled validators do not enter ToolDef.

The single Loop snapshot is keyed by registry pointer/revision, route, thin/stable/orchestrator/final-only modes, requested screenshot/headless tools and session-image presence on light routes. SetRegistry releases the old snapshot. A dedicated mutex protects snapshot reads, writes and resets, including the public estimator. Revision is checked again after building; a concurrently changed contract is returned for that current call but is not published under the old revision. There is no retry loop.

Every provider call receives an independent slice with exact len/cap, and immutable descriptor strings are shared. Provider mutation/appending cannot change the snapshot or registry. completeOnce still prices the actually passed definition snapshot, rather than replacing its cost with a potentially newer revision. No message history, prompt bytes, token-estimator semantics, model calls or OpenCode Zen path changed.

## Measurements

Median of three runs, 200 ms per subbenchmark. The uncached overlay replaces only the new build/estimate wrappers with the original uncached calls. It keeps the same current fixtures and registry types, allowing a direct comparison without Git changes. Raw logs and the reproducible overlay are under .tmp/runtime-context-2026-10-02-round3.

A warm sequence includes two next-request estimates, provider definition assembly, message preparation and the exact final request cost. This represents one step of actual coordinator preparation; it excludes serialization, network and model prefill.

| Warm request fixture | Time µs/op before → after | B/op before → after | allocs/op before → after |
| --- | ---: | ---: | ---: |
| thin=false/content | 27.86 → 8.74 | 35565 → 13289 | 19 → 14 |
| thin=true/content | 20.42 → 7.43 | 41726 → 17146 | 21 → 16 |
| thin=false/discard | 115.29 → 94.44 | 69746 → 47470 | 142 → 137 |
| thin=true/discard | 105.46 → 92.77 | 75907 → 51326 | 144 → 139 |
| thin=false/hidden | 47.52 → 28.25 | 37421 → 15146 | 25 → 20 |
| thin=true/hidden | 40.04 → 26.78 | 43583 → 19003 | 27 → 22 |
| thin=false/image | 55.34 → 35.13 | 45166 → 22891 | 23 → 18 |
| thin=true/image | 47.08 → 33.72 | 51328 → 26748 | 25 → 20 |

Plain warm preparation reduces allocations by 63% (full definitions) and 59% (thin). These B/op figures are allocation traffic, not RSS savings or end-to-end TTFT. In the mixed baseline CPU profile, body-count scans consumed about 39% CPU, tool-definition counting about 18%, and visible tool/definition assembly contributed 51.5% of allocation traffic across the synthetic benchmark iterations.

The miss fixture changes the screenshot-request flag before every request, so the key is rebuilt every iteration. The single-build benchmark intentionally omits the two estimates to expose the cold overhead rather than hide it.

| Cold case | Time µs/op before → after | B/op before → after | allocs/op before → after |
| --- | ---: | ---: | ---: |
| Full request sequence thin=false | 28.26 → 16.88 | 35564 → 21738 | 19 → 16 |
| Full request sequence thin=true | 20.05 → 11.92 | 41725 → 25595 | 21 → 18 |
| Single build + exact final cost thin=false/cold=true | 8.21 → 12.63 | 8448 → 11520 | 2 → 3 |
| Single build + exact final cost thin=true/cold=true | 4.63 → 5.89 | 8448 → 9216 | 2 → 3 |

Cold full request preparation still wins because the second estimate and wire assembly reuse the newly built snapshot. A standalone first/miss build is slightly more expensive: it computes the cached token cost and preserves the independent provider slice. This tradeoff is small in absolute CPU time and is explicitly measured.

### Resident cache

The fixture registers 63 visible tools; thin mode exposes 15 definitions. The uncached builder preallocates capacity for all visible tools, so the retained backing array is **cap × 48 = 3024 bytes in either fixture**, plus **88 bytes** of snapshot state in the Loop. Go allocator rounding makes the backing allocation 3072 bytes. Strings reference registry-owned immutable strings; schemas/descriptions are not copied. The registry adds one uint64 revision (8 bytes). Only one latest set is retained, and SetRegistry drops the old references.

The benchmark reports capacity, not length: length would wrongly report 720 bytes for the thin fixture while retaining capacity for 63. No extra cold shrinking copy is added in this change. The uncached overlay still includes the new empty snapshot field to compile the same benchmark; its reported state size does not mean the old implementation retained a cache.

## Validation

Scoped core and agent tests passed. Tests cover unchanged wire JSON, nil/empty behavior, provider mutation and append ownership, late registration, idempotent/missing mutations, Word activation/deactivation, route/thin/stable/orchestrator changes, screenshot/headless turn scoping, image add/remove/dormancy, final-only restoration, registry replacement and version changes during preparation. Two concurrent snapshot readers mutate their own result slices while a third goroutine changes registry activation; the final stable result and token cost match uncached preparation. These are ordinary tests; a Go race build was not run in this offline CGO-disabled setup.

The GUI context endpoint creates a separate Loop. The TUI runtime HUD obtains ContextReport after a completed turn, while the report reaches nextRequestTokenEstimate; the new snapshot nevertheless has independent locking so these public reads do not introduce races in the snapshot fields. Existing Loop ownership rules for history and route fields are unchanged.

Commands (use portable GOCACHE/GOTMPDIR/TMP/TEMP under the project .tmp, existing module cache, GOPROXY=off, CGO_ENABLED=0 and GOMAXPROCS=2):

```powershell
go test ./internal/tools/core ./internal/agent -run 'TestRegistryRevision|TestToolDefinition|TestFrozenCatalog|TestPreparedRequestEstimate|TestRequestEstimatePreserves|TestHeadlessSchemas|TestScreenshotSchema|TestLoop_BuildToolDefs|TestLoop_Thin|TestWorkerSchema|TestWorkerToolProfile|TestCachePrefix' -count=1 -p 2
go test ./internal/agent -run '^TestToolDefinitionSnapshotConcurrentRegistryUpdates$' -count=1 -p 2
go test ./internal/agent -run '^$' -bench '^BenchmarkPreparedRequestSequence$|^BenchmarkToolDefinitionSnapshot$' -benchtime=200ms -count=3 -p 2
go test ./internal/agent -run '^$' -bench '^BenchmarkToolDefinitionSnapshot$|^BenchmarkToolDefinitionSnapshotSequenceMiss$' -benchtime=200ms -count=3 -p 2
go test -overlay .tmp/runtime-context-2026-10-02-round3/uncached-overlay.json ./internal/agent -run '^$' -bench '^BenchmarkPreparedRequestSequence$|^BenchmarkToolDefinitionSnapshot$' -benchtime=200ms -count=3 -p 2
```

## Remaining candidates, not implemented

1. Long hidden/projected history still causes repeated O(history) body scans and view allocation for prune, compact and wire preparation. The profile above grounds the CPU cost; raw historical/projected benchmark data is in baseline.txt and static-candidate.txt. A generation-scoped prepared view could remove repeated work, but its invalidation would need to cover compaction/pruning/hiding, edited public Messages, reasoning scope/policy, persistence recoverability, image state and model/provider changes. No broad history cache is introduced without that separate correctness analysis.
2. Native reasoning validation reparses opaque JSON into RawMessage maps before transport assembly. Large native blocks can therefore be parsed/copied twice. This is a read-only observation, not a measured accepted change; avoiding it must preserve malformed-payload rejection, exact key/case behavior and provider-specific signatures. The current native replay behavior is preserved.

The neighboring DeepSeek projection-checkpoint implementation reviewed by the parent treats projections as disposable versioned derived data and falls back to exact replay. That supports explicit lifecycle invalidation, not persistence of an authoritative alternate history. No neighbor code or broad projection persistence was copied.
