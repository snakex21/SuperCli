# Visual folder indexing foreground priority

The active image-index path is folder_document_index.go -> describeIndexedImage, which writes a successful caption into the per-file cache. It previously labeled calls vision-index but omitted WithBackground, unlike document/folder text summaries. Merely adding that flag would introduce a second defect: Metered may close a preempted stream without forwarding an error delta, and the helper would accept and cache an incomplete caption.

## Change

The image helper change is limited to describeIndexedImage in folder_index_scan.go. Image analysis is background work. Each attempt adds an atomic cancellation observer through the existing additive WithCallSink API, preserving wrapper, session and out-of-turn metrics sinks. Metered emits its final CallStat before closing the output channel. Therefore, after the range completes, Canceled reliably identifies the preempted attempt even when its error delta was suppressed.

The helper discards that attempt's partial text and retries the identical already-prepared image/message through the existing idle/background gate. It retries only a confirmed canceled call while its parent call context remains live. All attempts share the original 90-second deadline; there is no new timer, queue, thread, persistent cache or model request for idle registration. Ordinary provider errors, including context.Canceled with no canceled metering stat, do not retry.

User/job cancellation or expiration of the shared call deadline returns an error before any caption is accepted. The existing indexing flow consequently leaves the interrupted image uncached; job-context cancellation returns from indexFolderPaths with no completion timestamp. A subsequent explicitly requested index can reuse completed file-cache entries and process the uncached image. If the per-image 90-second budget expires while the overall job is live, existing AnalysisFailed/Skipped/error reporting is preserved; this patch does not redesign partial-index job status or document-summary handling.

## Metered error-state prerequisite

The first retry fixture exposed a separate false positive in the early inner.Complete error branch. Background cleanup cancels its own derived context; reading ctx.Err() after that cleanup incorrectly reported every immediate provider error as Canceled. The retry prototype therefore retried ordinary errors. meter_metered.go now snapshots cancellation before cleanup, keeping the existing cleanup order, gates and Failed flag. A provider error equal to context.Canceled while the supplied context is live is not labeled as request cancellation. Genuine preemption before the stream still sets Canceled. This prerequisite makes the observer-based retry discriminate the actual condition; no blind error retry was retained.

## Verification

Dedicated tests exercise the actual Metered coordinator with deterministic stub channels, no network/model/app requests: silent stream close, cancellation error delta, and cancellation before Complete returns; successful resumed caption/cache path; unchanged pixel/message payload and deadline; chained wrapper/context metrics; parent cancellation with no saved image or completion timestamp; and no retry for normal or unconfirmed canceled provider errors. The separate canceled-parent fixture also catches the pre-existing acceptance of a partial silently closed stream.

Current-source scoped tests PASS: internal/llm 0.261 s and internal/webgui 0.484 s; scoped vet PASS. Baseline overlays independently reproduce both defects: canceled helper stream returned incomplete caption with nil error, and ordinary immediate background errors falsely set Canceled=true (including a context.Canceled error with a live request context). The new immediate-error tests verify the foreground path and both existing sinks too.

Artifacts: .tmp/goal-ui-round7-2026-10-02/image-preemption/{baseline-regression.json,meter-baseline-regression.json,scoped-final.json,scoped-vet.json}. Runtime evidence is deterministic stub execution, not a model latency benchmark.

## Limits

This fixes a demonstrated scheduling/correctness path for explicitly started visual folder indexing. Indexing does not start from a plain greeting. It does not explain or claim to improve the measured native LM Studio decoder change from roughly 35–36 to 27 tokens/s. Existing metering priority is process-local; separate GUI/TUI executables can still share one backend.

The preemption classification relies on the production Metered contract: with a live caller deadline, its private background context is canceled by foreground preemption. Bare test/custom providers that do not emit metered cancellation stats are not blindly retried. No native RSS/FPS/GPU throughput benchmark was run.
