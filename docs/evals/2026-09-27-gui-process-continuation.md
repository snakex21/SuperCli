# GUI process continuation audit — 2026-09-27

## Question and current code

After preserving process/worker metadata during context pruning, verify that a fresh GUI request can still use those handles. `Engine.processSessionFor` retains one manager per canonical workspace; `workspaceCacheKey` resolves aliases and folds Windows case. Worker state is likewise engine-owned, and discovered tools are restored from the session writer before execution. TUI registration keeps the manager with its long-lived application registry.

The suspected per-message process reset is not present in the current path. No production change was made for this hypothesis.

## End-to-end regression added

`TestWebProcessSurvivesNewRunWithoutRediscovery` runs two real `Engine.runStream` turns using scripted model responses and an actual helper process:

1. Discover `process_session` once and start the helper. A local socket handshake prevents output/exit before the first turn finishes.
2. Cancel the first request's context. Read the saved discovery names and verify the process manager is shared for an equivalent workspace path (including Windows case differences), but isolated from another workspace.
3. Release the helper and issue a second web turn on the same persisted session. The native tool schema is already restored; thin mode uses the existing dispatcher without discovery.
4. Await completion through the real process tool, and inspect the GUI terminal event's process ID, status, exit code and output.

All four native/thin and success/failure cases pass with one discovery, one process start and one completion wait. The test validates the selected thin/full prompt profile rather than just changing the scripted call syntax. It also checks the native schema on the second request. Failure exit 7 remains a UI tool error; exit 0 succeeds. No process polling or sleeps are used.

Existing worker-resume tests, exercised by the package suite, cover questions/progress and direct/dispatcher follow-ups on fresh web loops. This audit does not claim restart persistence for OS processes or workers; they are engine-lifetime resources. It does not measure provider latency or real-model decision-making.

## Validation

- Focused four-case integration test: passed.
- `go test -timeout=90s ./internal/webgui`: passed.
- `go vet ./internal/webgui`: passed.
- `git diff --check`: passed.

Artifacts: `.tmp/gui-process-continuation-2026-09-27/`. Only a regression test and evidence records were added. The existing verified CLI/GUI builds from the worker-pruning batch remain installed; no production instruction, schema, provider call, Zen behavior or storage path changed.
