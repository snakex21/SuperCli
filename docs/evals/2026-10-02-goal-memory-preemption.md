# Preserve memory when foreground work interrupts autosave

Baseline: b754435 (1.0.4-dev.10). Tested on Windows, 2026-10-02.

The TUI idle autosaver could accept a partially streamed background summary after foreground work preempted it. Metered cancels its private child context; the parent may remain alive, and a provider can close silently. providerSummarizer previously returned partial text successfully. StoreSummary also lacked checks for a canceled parent. This could persist incomplete project/user notes, advance covered history and delete raw startup history. A retry then made zero calls because the fragment was already marked covered.

The new focused tests failed against the baseline: both actual Metered foreground preemption and parent cancellation advanced coverage from 0 to 2, stored a partial task log, and prevented retry. A canceled raw pass called the callback twice and deleted both original entries. Pre-canceled StoreSummary also called its callback once. Empty and NOTHING partial results incorrectly consumed history; a partial USER line became a preference.

## Change

- providerSummarizer observes cancellation with an additive per-call metrics sink and checks the parent after the stream closes. It discards incomplete text without replacing existing sinks or retrying inside the helper.
- StoreSummary checks cancellation before inference and before interpreting or persisting its result. Complete NOTHING/USER behavior remains intact.
- SummarizePendingRaw stops an interrupted pass, leaves original entries available and returns a completion status. Ordinary provider errors still keep their entries and finish that startup pass, preserving the existing policy rather than scheduling endless retries.
- TUI startup marks a raw pass attempted only after an uninterrupted pass. An interrupted callback returns immediately; incremental helper work waits for the next idle window instead of queuing behind the foreground call. The original timeout is preserved.

No prompt, tool schema, model settings, foreground tools, OpenCode Zen transport, memory idle grace, deterministic user facts or exit path changes. A short personal declaration still persists; there is no chat-size threshold. All data remains in portable project/application stores.

## Verification

Controlled channels use the actual Metered coordinator, with no HTTP/model calls or polling. Tests verify silent partial delivery, live-parent preemption, parent cancellation, existing wrapper/context metrics parity, unchanged coverage, no partial persistence and one successful later retry. Storage tests cover empty/NOTHING/task/USER results, zero calls for already-canceled work, preserving two raw entries and stopping after one canceled attempt. Complete retries consume raw history. Ordinary provider errors retain their previous pass behavior; only an expired/canceled parent or a confirmed private interruption signals retry eligibility. Draining after an error remains bounded by the parent context even for a provider that does not close its stream. Ordinary live-context cancellation-shaped provider errors do not repeatedly schedule startup inference.

Focused app, storage/memory and llm suites pass. Private baseline and final logs are under .tmp/goal-background-round8-2026-10-02. This is a correctness and foreground-priority repair. It does not establish a Qwen decoder speed improvement or explain the observed 36 to 27 tokens/s regression.
