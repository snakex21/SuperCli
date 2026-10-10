# GUI Stop, command completion and attachment reads — 2026-10-09

This continuation closes two reported regressions: the GUI stayed busy after Stop or a completed answer, and a directory command could spend a long time preparing a project checkpoint before execution.

## Final behavior

- The frontend stops its animation on Stop or the new finishing event. Health refresh cannot resurrect the old busy state. A manually submitted next message and its attachments wait for a single durable completion receipt; earlier queued work stays paused. Rejected requests restore the draft instead of losing it.
- The agent consumer and Metered wrapper observe cancellation while waiting for input. They no longer require producer EOF. Metered drains only already-ready accounting with a bounded nonblocking budget, preserving terminal token usage without forwarding more answer text. Accepted partial replies are retained without dangling incomplete tool calls.
- Observed usage is counted before the error branch. Interrupted production session counter writes use a private SQLite connection with busy_timeout=0. Ordinary shared pool settings remain unchanged. Failed writes retain persistence-health reporting.
- Task capsules use cached stores, bounded dialogue excerpts, a private fail-fast SQLite transaction and the existing durable Markdown outbox. The turn tail does not render the Markdown mirror. Transcript persistence remains authoritative.
- Literal Windows dir and native cmd /c dir share a validated executable mapping. The checkpoint wrapper binds the exact native argv to the call context; the runner consumes that binding. Custom executables, environment overrides, operators, expansion and unknown arguments retain the ordinary checkpoint path. Tests include a real listing, a mutation and Undo.
- Mail attachment previews inspect the file signature before reading its body. Non-image PDF bodies are skipped; image payload and bounded-read behavior remain covered.

## Validation

Source SHA-256: 4e01855bf11948c0016aec08db45ae5ff464c306e41f6e7fb2f36004f392091f. Reconstructing originals and excluding the 13 new sources exactly reproduces installed l source 78d088515e517e26c757329f8e33dba26f6f7036911cc95690cebe496711a58c.

Fresh full tests passed for 12 packages: llm, agent, tools/mail, storage/memory, storage/session, webgui, app, ui/tui, tools, checkpoint, tools/ctxexec and tools/workflow. The same scope passed go vet. All 231 UI tests passed. Cancellation regressions passed 20 repetitions, including open producer channels and ready terminal usage. The first full integration run exposed buffered usage loss; the bounded accounting drain fixed it, and the final full suite passed. Held-writer tests validate fail-fast interrupted counters without changing shared SQLite busy_timeout=5000.

Ten TUI/GUI binaries built for Windows amd64, Linux amd64/arm64 and macOS amd64/arm64. Both local Windows EXEs were atomically replaced with recoverable backups, hash-verified and passed --help with exit 0. The manifest is .tmp/optimization-oct8-2026/final-oct9-m.json; source receipts and logs live in the same portable workspace folder.

## Limits

The screenshot's active 59.8-second invocation was not polled; its exact cause was not inferred from the timer alone. The identified pre-command checkpoint mechanism and both directory-command variants have independent regression coverage. Mock cancellation timings are not claims about real-provider throughput. Zen-specific routing and model tool schemas were not changed. No release, push or new background monitor was started by this continuation.
