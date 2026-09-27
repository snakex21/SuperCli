# Automatically hand off original worker output references

Date: 2026-09-27

## Confirmed gap

Workers already share the parent's bounded output store and can persist original tool results. However, their automatic evidence log consumed ToolResultEvent.Output only. That field is the UI text: it has neither the model-facing output handle nor extra RetainedText. The evidence log then takes a bounded excerpt itself.

Consequently, a coordinator could inspect only the truncated observation unless the worker model voluntarily copied its original output handle into its final prose. Existing shared-store tests explicitly made the model repeat that handle. This is a current-code reproduction of a separate handoff gap, not a claim that the shared store itself failed or that a particular saved session reran work for this reason.

## Change

Tool-result events now carry an internal OutputHandle obtained from the already-built model result. The source result is retained once, as before. Worker observations append the corresponding read_output reference after their bounded excerpt, including results whose additional text has no inline UI body. Success, failure and interrupted-result paths retain the reference.

The handle is excluded from event JSON; UI Output text and the worker's own provider result remain unchanged. The normal completed-result event is emitted after the existing retention step has produced its handle; no extra store write or provider request is added. Thus an existing slow persistence write can precede the completion event instead of following it. Tool-call/start events remain immediate.

Only a reference added by output retention is forwarded. An unchanged result containing a lookalike footer, or read_output content that quotes an older footer, does not create new trusted reference metadata.

The automatic handoff contains tool observations and references only, not private worker briefing, reasoning or conversation. Existing observation, cache and persistence bounds still apply; expired/evicted output handles retain their existing behavior.

## Verification

- Sixteen native/thin, memory/persistence, large/attached/reference-only/failure regressions fail before and pass after the change.
- The worker's final report deliberately contains no handle. The coordinator recovers an original middle detail omitted from both the report and bounded observation, with one source execution and exactly two worker model requests.
- Persistence variants use a fresh parent loop and registry, so original and aggregate output references must resolve from stored output rather than the worker's live cache.
- Interrupted-result reference recovery, unchanged event JSON, lookalike footer rejection and evidence size bounds pass.
- Existing worker evidence, output sharing/persistence, cancellation, UI and full project tests pass.
- go test -timeout=90s ./..., go vet ./..., CLI/GUI builds, CLI --help and git diff --check pass.

## Costs and limits

A reference adds a small line to the bounded observation. Large observations remain stored behind the existing handoff handle; tiny ones may include the line inline. The fixtures' coordinator-facing handoffs are 524–644 bytes. A borderline inline observation can cross the existing 1 KB boundary and become attached; the threshold has not been expanded.

The change enables retrieval without another worker inference or rerunning the original tool. The deterministic replay proves availability and call counts, not a live-model latency improvement or a guarantee that every model will choose the reference.

Artifacts: .tmp/worker-evidence-reference-2026-09-27/ contains red/green replays, guard tests, full validation/build output, source-before copies and installed binary hashes.
