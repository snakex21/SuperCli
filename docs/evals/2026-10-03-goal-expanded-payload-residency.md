# Expanded GUI payload residency — 2026-10-03

Base: 60f4e91 (dev.12). A saved tool row keeps its cancellation handle in _cancelHistoryPayload. After expansion, renderToolPayloadWhenOpen removed its toggle listener but the retained cancellation closure still referenced the rendering closure, including the full original arguments and output. For command results, that could keep encoded JSON after its decoded stdout/stderr had already been materialized in the visible DOM. Canceling the deferred renderer also retained its no-longer-usable rendering closure.

The helper now clears its render reference in finally after the existing one-time callback, and when canceled. Folded rows retain the callback until it is needed. Original exception propagation, consumed-once behavior, listener ownership, lazy display, cancellation, reopening, boundary-call resolution and complete visible output remain unchanged. There is no cache, timer, service, provider instruction or persistence change.

## Reachability and generated fixture

A dependency-free DOM double uses current production buildHistoryFragment and appendHistoryToolPayload with generated escaped command JSON. WeakRefs observe the actual production render callbacks while rows and their cancellation functions remain retained. The source snapshots and loader intercept actual production reads. No private prompts, user applications, browser processes, images or providers were accessed.

| Generated command rows | Encoded JSON characters | Callbacks while folded before/after | After expansion before/after | After cancel before/after | Expanded V8 heap before / after | Observed difference |
|---:|---:|---:|---:|---:|---:|---:|
| 8 | 54,504 | 8 / 8 | 8 / 0 | 8 / 0 | 6,231,440 / 6,175,944 B | 55,496 B |
| 20 | 4,610,910 | 20 / 20 | 20 / 0 | 20 / 0 | 16,147,664 / 11,408,992 B | 4,738,672 B |

The decoded full stdout/stderr content, expanded DOM hashes and node counts match before and after. Reopening creates no duplicate viewer. Callbacks are the ownership evidence; heap figures come from one Node v24.15.0 before/after fixture pair with explicit GC and include surrounding fixture objects, allocator and GC variation. Content and byte sizes are generated stress shapes, not a sample of typical saved command output. They do not establish native WebView2 RSS, browser layout/paint cost, FPS, input latency, model tokens or provider throughput. Full rendered DOM and its required source remain; only the consumed/canceled closure is released.

## Verification

Three permanent lifecycle regressions cover deferred and initially-open callbacks, reentrant toggle delivery, unrelated listeners, Unicode/newline output, reopening, idempotent cancellation, a detached/reset row, and original errors in deferred/immediate callbacks. A child Node process with explicit GC confirms that a retained cancellation handle no longer retains a consumed callback/source carrier while folded callbacks remain usable. Its reachability assertion fails on the baseline; the behavior controls pass on both implementations.

All 167 frontend tests pass on the final production source (863.8 ms), including ordinary no-history/no-image stream and reasoning transitions, pacing and scrolling, complete Markdown, initial/older history paging, open boundary-result resolution, worker links and media previews. The final helper source is byte-identical to the isolated overlay that previously passed all 164 existing UI tests. Owned diff checking passes. Full application integration belongs to the main agent.

Portable ignored evidence: .tmp/goal-ui-round10-2026-10-03/REPORT.md, source overlays, history-payload-retention.cjs, before/after receipts, overlay-source-read log, baseline lifecycle failure, final UI receipt and frozen source hashes.
