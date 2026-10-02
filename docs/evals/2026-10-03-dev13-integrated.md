# Integrated optimization round — dev13

Date: 2026-10-03. Baseline: main 60f4e91, dev12. New local binaries identify as 1.0.4-dev.13; stable buildinfo source remains 1.0.3. The optimization goal remains active.

Three verified changes are integrated:

- TUI completed-history append reuses a byte-exact rendered prefix only for unchanged built-in rendering inputs, with cloned immutable message-header snapshots. A generated 2400-message add-result→refreshTranscript→SetContent→View test improved 131.65–150.13ms→6.36–6.48ms; temporary Go allocations 59.01MB→3.01MB per operation. The metadata adds 175232B at this history length. See [TUI evidence](2026-10-03-goal-tui-history-append.md).
- GUI lazy payload releases consumed or canceled render closures while folded callbacks remain available. A generated 20-row fixture retained 4,738,672B less V8 heap after explicit GC; full decoded DOM matches. This is not measured native WebView2 RSS. See [GUI evidence](2026-10-03-goal-expanded-payload-residency.md).
- Interrupted large tool output attaches existing bounded persistence, so read_output can retrieve omitted diagnostics in a fresh loop. Real saved evidence contained a 61,933-byte memory-only archive with no persistent copy. This is recovery reliability, not a measured saved model turn. Cleanup can wait up to the existing one-second timeout when storage fails. See [recovery evidence](2026-10-03-goal-cancelled-output-persistence.md).

## Integrated verification

The final source guard covers 1821 files under cmd/internal/test and go.mod/go.sum, unchanged through checks, all builds and installation. Full Go test passes 70 test-bearing packages (28 no-test packages), go vet passes, diff checking passes. All 167 UI tests pass in 834.4908ms. Scoped TUI full tests pass in 4.614s, its vet passes, and an independent reviewer found no correctness blocker. The production TUI renderer/helper match the gofmt-normalized measured overlay exactly.

Ten CGO-disabled builds succeed: CLI and web GUI for Windows amd64, Linux amd64/arm64, macOS amd64/arm64. Only native Windows version/help commands were executed; foreign binaries were cross-built, not launched. There were no model generation calls, screenshots, user-app controls, provider/LM Studio/hardware setting changes or OpenCode Zen path changes in this round. No release, tag or updater publication was made.

Installed Windows CLI 26245120B SHA256 102c1919ce7f92238fdb73de906909139a311cb699ea2b4426171297afe1eb4d; GUI 22958592B SHA256 729084f9800c9865f3e95af3e88b0a5b236efefb2c144b4c8494d207025f0770. Atomic rename/copy verification and backups stay under the portable ignored integration directory. CLI reports 1.0.4-dev.13; both help paths pass. A previously running GUI requires a normal user restart to load the embedded frontend changes; no app was force-closed.

The human-reported Qwen reload recovery remains supported by the bounded earlier observation of 34–35 decode tokens/s, but this round does not establish a new matched native inference comparison or root cause of the prior slowdown.

## Deferred audit leads

The neighboring-harness/runtime audit found six saved read_lines unknown item rejections. Automatic repair was rejected because the field could encode range intent and guessing/dropping it would weaken correctness; no six-turn saving was established. A conservative short ctx_execute command-echo prototype found 318 eligible successful results across six bounded sessions, removing 52,187 model-text bytes (median 156B/result). Its two extra encodings add CPU/temporary allocations; token/prefill benefit is not measured. It remains an isolated ignored prototype for possible later comparison, not part of this production change.

Receipts and cross-platform binaries: .tmp/goal-integrated-dev13-2026-10-03. Redacted detailed evidence and overlays: .tmp/goal-tui-history-round10-2026-10-03, .tmp/goal-ui-round10-2026-10-03, .tmp/goal-runtime-round10-2026-10-03, .tmp/goal-harness-round10-2026-10-03. No private prompts, handles, raw logs or screenshot pixels are included in this report.
