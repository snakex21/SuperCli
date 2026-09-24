# Faster startup context collection

The optional repository briefing ran before the first project request. Outside a Git repository it walked every file, performed an additional stat, retained all entries and sorted them just to report ten recent paths.

The shared preflight now samples at most 2048 directory entries with a 200 ms cooperative scan deadline, reads directories in batches, reuses enumeration metadata, and retains only the ten newest files found. A partial sample is explicitly labeled; it never claims to identify the newest files across the entire tree. Full search tools retain their existing behavior. Blocking filesystem operations cannot be interrupted in the middle of an OS call.

Git status is collected concurrently with branch/log lookup. Status remains fresh, and a failed status is never presented as clean. Real Git processes share a five-second deadline, disable optional index locking, and observe foreground cancellation in the GUI. The CLI/TUI, GUI and workers use the same collector. No provider-specific path, prompt instruction or helper inference was added; OpenCode Zen transport is unchanged.

## Local measurement

Windows, Ryzen 7 5800X3D; synthetic non-Git tree of 120 directories and 12000 files. Three one-iteration samples, with fixture creation excluded. The optimized collector intentionally returns a labeled sample rather than scanning the entire fixture.

| Optional collection step | Before | After |
| --- | ---: | ---: |
| Median duration | 468.65 ms | 25.64 ms |
| Median allocated bytes | 12839232 | 1153432 |

This measures startup collection only, not model loading, prompt evaluation, reasoning, network latency or total first-response time. No live model benchmark was run for this change.

## Validation

- Bounded traversal, shared ignored directories, wide folders and cancellation.
- Small-tree newest-file selection and truthful partial-scan labeling.
- Overlapping Git reads, canceled subprocess startup, failed-status handling.
- GUI integration: persist project facts and prior session results, close and reopen the engine, start a new conversation. Its first request contains the earlier facts/results with exactly one foreground model call; archived messages remain intact.
- All 64 tested packages passed via go test ./...; go vet ./... and git diff --check passed.

Raw runs are in .tmp/startup-before.json, .tmp/startup-after.json and .tmp/startup-checks.json.
