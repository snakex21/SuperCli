# Integrated development build 1.0.4-dev.18

Base: main 9a930aab920c58b47f7771bd35d4855e51386d4a (dev17). This tranche combines tool-image presentation, portable disclosure preferences, and a measured exact-name dispatch change. It adds no standing model instruction, model request, service, timer, dependency or database migration.

## Result

Images supplied as tool analysis inputs now render inside their originating expandable tool card. A folded card creates no image element, source or image request; the existing lazy payload helper loads its bounded thumbnail on opening and retains the original viewer. Explicit presentation outputs such as show_media and canonical screenshot/generation outputs keep their visible chat preview. Real user attachments are unchanged.

History association uses explicit host-only tool-output provenance and exact source call IDs, including reused IDs and origins on older pages. Loading an older origin transfers the existing preview node. Missing origins retain a temporary tool preview card. Legacy, mixed, unsafe or unmarked carriers retain the existing attachment behavior rather than inferring intent from content. The explicit history payload completion guard preserves raw tool output when a transferred preview arrives before native toggle handling.

Tools and thinking remember the user's expansion preference through the existing portable UI settings blob. Only user activation of an outer summary or an existing bulk shortcut saves it; rendering, restoration, nested controls and programmatic toggles do not. Unset thinking preferences preserve previous live/history defaults. No new controls or browser-storage writes were added.

Provenance adds an optional source call ID and carrier flag to ImageRef. The 64-bit structure grows from 104 to 120 bytes; the flag uses existing padding in the origin-only layout. There is a small persisted/DTO metadata cost. Real attachment admission clears these host-only fields on an owned copy. Exact request-builder controls cover existing Chat, Anthropic, Codex, Responses, Zen Responses and Echo routes with native signed/encrypted state, vision on/off and file/data/URL image inputs. Provider implementation files, media bytes and tool descriptions are unchanged; this is not a live-provider performance comparison.

Known registered tool dispatch no longer allocates and scans the full registry name slice before hardening. Missing names retain the complete existing diagnostics with one extra lookup. One bounded ABBA on actual Loop.invoke with 64 registered tools measured 2531–2625 to 1681–1765 ns/op, 2824 to 1672 B/op, and 21 to 20 allocations/op. Small-registry timing benefits are limited; miss/count1 increased from 1014–1027 to 1032–1058 ns/op. A fresh controlled-clock before/after pair has identical complete request/history/semantic-event fingerprints in all seven cases. No production clock, policy, activation, schema, permission, retry or cancellation logic changed.

## Completed validation

- Full go test ./...: 71 test-bearing packages PASS; 28 packages without tests.
- Complete Node UI suite: 188 tests PASS, zero failures/cancellations/skips; 898.407 ms reported suite duration.
- go vet ./..., formatting of the ten owned Go files, and git diff --check: PASS.
- Owned isolated headless-browser fixture: folded image has zero DOM/request cost; real mouse/Space summary activation persists once; programmatic reopening persists nothing and retains image/viewer identity; explicit presentation does not duplicate; bulk closing two cards saves once. All owned browser contexts were closed.
- CLI and GUI builds with CGO_ENABLED=0: Windows amd64, Linux amd64/arm64, macOS amd64/arm64, ten successful builds. Cross-compilation is not target-platform runtime verification.
- Frozen owner manifests: 21 unique source/test/scope-report paths verified, with only the approved Names hunk combined into the shared loop_tools.go. All 1850 cmd/internal/test and module-file hashes are identical before checks, after checks, after builds and after the installation attempt.

No live model POST, GPU experiment, user application intervention or provider setting change was needed for these controls. No user application/profile was opened; existing toolchain/module files were read only. Fixture/cache/temp/build/backup evidence stays inside the repository's ignored .tmp folders. No WebView2 process RSS, end-to-end prefill, model TPS or required-turn improvement is claimed; folded preview loading and the conditional dispatch allocation reduction are the demonstrated effects.

## Local binaries and installation

The Windows dev18 CLI is installed. Its --version reports supercli 1.0.4-dev.18 and --help completed successfully. The GUI dev18 artifact is built and staged, but Windows rejected replacing the currently open GUI executable (EPERM). The installed GUI remains the verified dev17 binary; no app was closed or terminated and no repeated replacement was attempted. Installation resumes after normal user closure. The full installed GUI help check has not run.

| Windows artifact | Bytes | SHA-256 | State |
| --- | ---: | --- | --- |
| supercli.exe | 26313728 | 883a9ab35d0e2040ee1db2f627039684eedcf51aba9930e6c784536432a6b212 | dev18 installed |
| supercli-web.exe | 22999552 | d1eabe90ed4759b475def36b1da4cfdffc963fa5aa0ea26064c0d5e60e3602a6 | dev18 staged; installed GUI is dev17 |

The CLI is 512 bytes larger and the GUI 9216 bytes larger than dev17. This is a development build, not a new public release/tag. Root evidence is .tmp/goal-integrated-dev18-2026-10-03, including checks-summary.json, built-artifacts.json, source snapshots, scoped-frozen-sources.json, installation.json and installed-state.json.

## Focused reports

- [Host-only image provenance and provider parity](2026-10-03-tool-image-provenance.md)
- [Tool-read image disclosure and portable preferences](2026-10-03-tool-read-image-disclosure-ui.md)
- [Known tool dispatch controls, allocation benchmark and miss tradeoff](2026-10-03-goal-known-tool-dispatch.md)
