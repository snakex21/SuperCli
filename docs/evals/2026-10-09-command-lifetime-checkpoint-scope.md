# Checkpoint scope reuse and caller-owned command lifetime — 2026-10-09

This completes a validated follow-up to 2026-10-09-checkpoint-worker-foundation.md. The optimization goal remains active. No provider token-rate, prompt-processing, end-to-end agent-task, or application RSS improvement is inferred from these local measurements.

## Avoid repeated capture of absent paths

A touched, narrowly scoped turn with a successfully published live before-pin can extend its scope when fresh Lstat checks confirm every newly selected root is absent. It keeps the immutable original before object and active pin; it does not recapture Git state, rewrite refs, or transact accounting for that extension. Existing empty files/directories, whole-workspace snapshots, initial capture, cancellation and errors retain ordinary capture behavior. New roots are still collected by the final after capture. Empty initial trees skip an unnecessary update-index invocation.

A failed partial publication marks the baseline unpinned. Covered/whole mutations and finalization must repair its original pin under the store gate before proceeding. They never substitute the now-modified workspace for the initial bytes. A missing original object blocks mutation. Tests exercise real Git pruning, CRLF/binary preservation, creation absence, Undo/Redo, cancellation, failed scoped/whole retries and finalization after partial publication.

BenchmarkAbsentScopeExpansionCreate64: Windows amd64, Ryzen 7 5800X3D, GOMAXPROCS=2, three runs with three iterations each. Both variants create the same 64 fresh 1-KiB files, preserve the same before object and 65 selected roots, and complete a verified 64-file record. The baseline forces the existing normal capture path for each scope extension. Initial capture, setup, file removal, and finalization are outside the timer; native file writes are inside.

| Median | Capture each extension | Reuse live before-pin |
| --- | ---: | ---: |
| ns/op | 15,689,140,733 | 95,743,900 |
| Allocated bytes/op | 32,779,501 | 1,373,874 |
| Allocations/op | 244,448 | 15,750 |

The timed workload is about 99.39% shorter and allocates about 95.81% fewer bytes. This is a specific absent-file scope workload, not a total checkpoint/task/RAM measurement. Receipt: .tmp/optimization-oct8-2026/go-absent-scope-bench-oct9-0.log. The later pin-repair finalization guard is outside this measured region; the timed reuse path remains unchanged.

## Commands and downloads do not acquire a default runtime deadline

- ctx_execute: omitted/zero timeout_ms passes through caller lifetime without allocating a timer. Positive timeouts remain supported above the former 300,000-ms maximum, bounded only by representable duration; negatives and overflow are rejected. User cancellation and caller deadlines still propagate. The description retains builds/tests terms so discovery remains selective.
- ctx_execute cancellation owns a Windows Job Object or a Unix process group. The scope is published under a mutex before Cancel can act. A native helper signals descendant readiness through a socket; cancellation returns and the descendant connection closes. The existing 1-second inherited-pipe WaitDelay remains a capture guard, not a running-command timeout.
- A deadline firing after native exit while inherited output is draining does not become a command timeout. Classification uses cancellation actually observed by Cancel; native exits 0 and 7 survive a deadline inside pipe drain. Actual cancellation/startup failure still reports its cause.
- TUI !commands: no default 30-second deadline. Explicit Runner.Timeout remains available. During execution, stdout retains 12+4 KiB and stderr 2+2 KiB plus one omission marker, replacing unbounded buffers. A native helper produces 8 MiB per stream and verifies retained endpoints/bounds.
- process_session: omitted/zero lifetime_ms runs until process exit, explicit stop, or manager close. Positive optional lifetimes no longer clamp to 24 hours. Output retention and active-process count remain bounded. Cancellation of a wait does not silently stop a deliberately backgrounded process.
- web_download: no total 120-second context/client deadline for the transfer. Dial/TLS/header limits remain (10-second connection/TLS, 30-second response headers). Caller cancellation, safe public-IP dialing, redirect/size validation, bounded streaming and atomic publication remain. Slow-body and canceled partial-body fixtures confirm behavior; cancellation removes the partial and never publishes it. Their short artificial timing does not constitute a multi-minute network benchmark.

A real 31-second ctx_execute command completed without an explicit timeout; explicit timeout/caller cancellation tests passed. Session audits found successful asset downloads; they did not establish that a particular historical download was killed after five minutes. The code ceiling was directly established and removed. No new inference or provider calls were needed for this batch.

## Exact local artifact cleanup

Removed 40 obsolete .tmp binary files, 957,555,428 logical bytes (about 0.892 GiB), after historical receipt SHA/size binding and exclusive native-handle verification. A sealed allowlist pins directory ancestors, rejects reparse points/hardlinks/alternate streams/readonly/locked files and verifies the same handle before deletion. No directories were deleted. All 40 paths were verified absent after completion. Ten candidates lacking historical content hashes were excluded. Go caches, project files, live/recoverable checkpoint history and current rollback copies were retained. This is disk reclamation, not RAM savings or a census of the current entire application folder.

Receipts: native-cleanup-oct9-completed.json and native-cleanup-oct9-apply-ae41dc902c2a44788cc8216798512ef1.jsonl in .tmp/optimization-oct8-2026.

## Final validation and install

Source hash: a6fc1247846327836e467b1ccbd3536a7b553e28a68617f03eb1b793f46c894e.

All 99 Go packages are covered: 71 test-bearing packages passed and 28 have no tests. The checkpoint package ran in three completed chunks covering exactly all 176 listed Test/Example/Fuzz top-level cases; exact case and package unions were checked. go vet passed. UI: 218 passed, zero failed. A tool-deadline-interrupted whole checkpoint invocation was not accepted as completion proof; the final chunked runs supply proof. A discovery regression from removing builds/tests words was fixed and the final source set was fully revalidated.

GUI/TUI built for Windows amd64, Linux amd64/arm64 and Darwin amd64/arm64, with source hash verified at each stage and all 10 output hashes verified before installation. Cross-compilation does not establish native Linux/macOS runtime behavior.

Both Windows EXEs were staged, SHA verified, installed with portable recoverable backups and passed --help smoke tests. No user process was closed or restarted. An already running process continues its loaded version until its normal next start.

| Installed executable | Bytes | SHA256 |
| --- | ---: | --- |
| supercli.exe | 26,913,280 | 852db0e8a88e79bc169135325738d3c960e85550b825c3eca42256fc20410ef2 |
| supercli-web.exe | 23,835,648 | 48e405152022b7a64067e100b0599beca170e0dd38b02efed78b06e0c438c1c5 |

Portable final receipts: validation-oct9-h.json, validation-oct9-h-pending.json, checkpoint-test-chunks-oct9-h.json, final-oct9-h.json, help-smoke-oct9-h.json in .tmp/optimization-oct8-2026. Special OpenCode Zen route source stayed unchanged. No commit, push or release was made during this batch.
