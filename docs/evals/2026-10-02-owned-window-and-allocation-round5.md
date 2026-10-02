# Owned application screenshots and bounded output reuse — round 5

Date: 2026-10-02. Parent source: 4b6e9f9. This round retains WebView2 and the existing OpenCode Zen transport.

## Correct capture subject

The reported defect was a subject-selection/integration gap: a screenshot of the current compositor can photograph the user's browser/game instead of the program being tested. `screen` remains the explicit currently-visible-screen option. `process_session screenshot` now binds capture to a live session-owned native PID, after starting the GUI executable directly. `send_screenshot process_id` also scopes existing-window selection. Both can be narrowed by window title; ambiguity/errors never fall back to screen.

Native Windows capture uses one hidden, deadline-bound helper, a bounded startup input-idle wait, and process/window ownership checks. The requested process handle stays open during capture. Input-method/tool helper windows do not become automatic PID targets. No activation, restoration, host input, polling service or persistent image cache was added. Files stay in the existing portable snapshots directory. GUI live/restored previews remain lazy; ordinary process snapshots bypass the media JSON decoder; display-only captures do not upload pixels to the provider.

A real public-tool Windows fixture test starts an owned child GUI window, captures it by returned session ID and verifies the exact red/blue pixels, PNG metadata, portable data location and unchanged foreground. The separate minimized classic-window case returns an error rather than incomplete pixels; not every minimized/GPU application supports PrintWindow. Unit cases cover ended/stopped/unknown sessions, cancellation, atomic OS-exit-before-output-drain and metadata/image forwarding.

A second read-only native fixture uses a proper Windows GUI subsystem EXE and delays window creation by 400 ms. Public start with yield_ms:0 followed immediately by screenshot, without parent readiness/event/output waiting, captured the correct PNG in 0.44 s with foreground unchanged. This verifies the bounded input-idle startup path for that case; frameworks becoming input-idle before late window creation, or taking more than two seconds, remain unproven.

A single local Qwen3.8-27B tool-selection check after a synthetic launch chose `process_session {action:screenshot,id:proc-1}`, not screen, using production schemas. 1,312 input/104 output tokens, 9.213 seconds. No model-proposed process was executed in that check. This is functional selection evidence, not a latency/token/RSS A/B benchmark.

## Reuse already encoded command streams

`ctx_execute` already marshals its Result for UI/storage. The bounded success-preview helper now borrows the canonical stdout/stderr slices from those owned encoding bytes instead of marshaling both entire strings again. The guard falls back for different layouts; the scanner is explicitly not an untrusted JSON validator. The final bounded preview owns its bytes, and source/result data remain unchanged.

450 differential cases cover control bytes, invalid UTF-8, Unicode, quotes/backslashes/HTML escapes, two unequal streams, warning/truncation state, fallback and source immutability. Preview text/token behavior is byte-identical.

Three-run fixture measurements on this Windows host:

|Already-encoded JSON|Legacy helper median|Reusing encoding median|Allocated bytes/op|
|---|---:|---:|---:|
|8,723 B|32.9 µs|26.7 µs|21.2→11.6 KB|
|34,451 B|64.3 µs|38.8 µs|50.8→12.7 KB|
|137,363 B|184.5 µs|85.1 µs|158.3→12.7 KB|

The complete marshal+public-string+preview fixture at 64 KiB input measured median 742→294 µs and 494→334 KB allocated. Timing was variable; at 16 KiB the complete-path CPU result was noisy, so no reliable speed claim is made there. These are allocation measurements, not a decrease in application RSS. Raw marshal/storage and provider inference remain. Saved-session evidence found 23 retained structured results above 4 KiB, without claiming every historical result passed through today's preview helper.

The [native-validation report](2026-10-02-goal-native-validation.md) separately records identical request bytes/tokens and lower transient work on large saved contexts.

## Integration checks

After the source freeze, all 70 Go test packages, 156 UI tests and repository-wide go vet passed. All 1,781 source/check-fixture file hashes stayed unchanged during tests and builds.

Ten binaries compiled successfully: CLI and GUI for Windows amd64, Linux amd64/arm64 and macOS amd64/arm64. Cross-platform compilation does not establish native runtime window capture on Linux/macOS; those platforms keep the explicit WebDriver/QMP adapters.

The Windows CLI and GUI were installed as 1.0.4-dev.8, after portable backups of the previous EXEs. Installed CLI --version and both --help paths passed; SHA-256 matches the verified builds. Sizes are 26,207,232 B (CLI) and 22,934,528 B (GUI), respectively 28,672 B and 20,992 B above dev.7. No stable release/tag/updater metadata was changed. Checks, source guards, build hashes and installation receipts are saved under the ignored .tmp/goal-integrated-dev8-2026-10-02 directory.
