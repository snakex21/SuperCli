# Integrated dev9 validation — 2026-10-02

Base: 9fab896. Installed local Windows version: 1.0.4-dev.9. Stable release metadata remains unchanged; no release/tag/updater publication is part of this checkpoint. All build/cache/temp/evidence/backup writes are inside the portable application repository.

## Changes and evidence

- Requested screenshot/process/headless contracts use the existing dispatcher and request-only trailing context in thin + stable coordinator mode. Core wire definitions remain unchanged between task types. One controlled serial Qwen pair reduced newly evaluated tokens from 5,364 to 1,106 and first output from 18.816 to 4.234 seconds. Total input was nearly unchanged (5,364 versus 5,343). Parameters, warm-up asymmetry, additional wire bytes and limits are documented in [the prefix evaluation](2026-10-02-goal-requested-tool-prefix.md). Later shorter descriptions are not included in those model timings.
- Five tool descriptions preserve argument types/defaults/constraints and save 628 serialized bytes when included together. Only the three core descriptions are ordinarily in the fixed core prefix; media/process contracts appear on demand. [Description review](2026-10-02-tool-descriptions.md).
- Windows source:desktop captures wallpaper/icons without covering applications, changing focus or falling back to the visible screen. The authorized actual capture took 458 ms (3840×2160, 782,051-byte PNG, attach:false), with foreground unchanged. Existing source:screen and owned-window capture remain separate. Uniform wallpapers are accepted, untouched/incomplete buffers rejected, and known split WorkerW layouts fail explicitly. Live and restored chat preview fixtures include desktop output. [Capture behavior](../app-window-capture.md).
- HeadTail counts omitted newlines directly in the string instead of allocating a byte copy of all omitted output. The 16 MiB fixture goes from about 16.8 MB to 4.95 KB allocation per call with identical output; scanning remains necessary. [Evaluation](2026-10-02-head-tail-no-copy.md).
- TUI active transcript assembly reserves one exact combined buffer, retaining callback order and output. The changed-spinner refresh fixture saves roughly half its temporary allocation when the prior builder would grow. This is conditional and does not measure process RSS or provider latency. [Evaluation](2026-10-02-goal-tui-stream-assembly.md).

## Verification

The initial full go test ./... -count=1 passed 69 packages and found one exact wording expectation in TestAskUser_Spec. The final description retains the expected “instead of guessing” phrase; go test ./internal/tools/interactive -count=1 then passed. A fresh 1,838-file source/hash inventory proved that this description was the only source change since the full suite. Together these runs verify all 70 test-bearing packages in the final tree. Full go vet ./... and git diff --check passed. No redundant repeat of the other 69 unchanged passing packages was needed.

All 156 UI tests passed after adding live/restored source:desktop fixture cases. Scoped agent/media/TUI regression and native owned-window fixture tests passed before integration. Public agent Run tests cover desktop dispatch, owned start→screenshot→reply, exact contracts, native/parts history preservation, restricted/fallback modes, target validation, context accounting and per-run cleanup.

Ten binaries compiled: CLI and web GUI for Windows amd64, Linux amd64/arm64 and macOS amd64/arm64. Foreign platforms were cross-built, not executed. Windows binaries were atomically installed from the frozen build, with prior binaries backed up inside repository .tmp. Installed --version reports 1.0.4-dev.9; CLI and GUI --help passed. SHA-256 of both installed executables matches the built artifacts:

| Windows artifact | Bytes | SHA-256 |
| --- | ---: | --- |
| supercli.exe | 26219008 | 490730bd267b8e56a42c534dfcd6cf93bc252fbad497c042d470286395443bae |
| supercli-web.exe | 22945792 | a761fe5ae61eb39838fcacec0fce2edfa0ea613d9223bf4a02968f68d0dacc61 |

The source/hash guard was unchanged throughout builds and installation. Ignored artifacts are under .tmp/goal-integrated-dev9-2026-10-02 (checks, UI results, source inventories, ten build hashes, executable backups and installation proofs). No private session payload, screen pixels, config secrets or LM Studio raw logs enter tracked reports.

## Limits

The Qwen measurement demonstrates warm-prefix reuse for one synthetic desktop request pair on the current device. It is not a universal timing/billing guarantee or proof that cold startup, unrelated route changes, other models or all workloads are faster. No new RSS measurement is made; allocation reductions are distinct from persistent application RAM. Native desktop-only capture is Windows-specific and does not support every custom shell topology. Existing provider transports, Zen special behavior, model settings and native reasoning contracts are preserved. The broader optimization goal remains active.
