# 2026-10-01: browser handoff and remaining render costs

## Default browser and link destination

The reported URL was https://snakex21.github.io/snakex21/gry/gry.html. Windows' read-only HTTPS association lookup identifies Opera GX. A direct call to the previous ShellExecuteW opener succeeded (native result 42), so the original GUI failure was not reproduced and is not attributed conclusively to COM.

The Windows opener now uses ShellExecuteExW from an OS-thread-locked worker with STA COM initialization and balanced cleanup. It requests no process handle, does not execute a command shell, and reports native errors through the application. This follows Microsoft's ShellExecuteEx guidance for COM shell extensions and threads without a message loop:
- https://learn.microsoft.com/en-us/windows/win32/api/shellapi/nf-shellapi-shellexecuteexw
- https://learn.microsoft.com/en-us/windows/win32/api/shellapi/ns-shellapi-shellexecuteinfow

The browser HTTP endpoint no longer discards the native error. Both the main GUI and the independent viewer show the localized failure notice followed by the actual diagnostic, using text nodes.

An ordinary HTTP(S) link click in the conversation opens a small destination dialog with the existing translated preview/browser labels. Cancel does not create a frame or launch an application. The preview reuses one pane/frame, and Escape closes the dialog while preserving an existing preview. The per-link choice is also available in restored conversation messages through one delegated listener.

Native Windows/WebView2 fixture: 17 checks passed, including an actual browser handoff of the reported URL, two blocked foreign-origin API requests, an app-framing redirect blocked, no preview before use, preserved chat draft, cancellation, and preview reopening. The native API accepted the default-browser handoff; this does not assert that the remote page loaded successfully in Opera GX.

SHELLEXECUTEINFOW size/offset checks passed in actual 64-bit and 32-bit Windows tests. Focused viewer tests cover stale navigation, disposed controllers, one pending browser launch, error details, and language refresh.

## GUI paragraph append

Appending ordinary single-line prose to an already formatted paragraph now retains its completed HTML. Markdown delimiters, link-closing brackets/parentheses, end-sensitive italic markers, block conversion, CR/NUL, and replaced prefixes use the original complete renderer. The earlier reasoning-prefix cache prototype was rejected because its prefix validation cost exceeded the original regex scan.

A native WebView2 fixture with 80 synthetic packets at 20 ms measured renderAssistant p95 of 0.9 → 0.4 ms for an ~80k-character paragraph and 2.7 → 1.1 ms for ~300k characters. All 190 prefix/recovery comparisons and the final DOM matched. Both baseline and candidate had no long tasks or frames above 34 ms in this fixture. This reduces local rendering work; it does not demonstrate a change in provider latency or eliminate sparse provider packets.

The combined UI suite passes 86 tests. Full webgui package tests pass.

## TUI render reuse

Two bounded entries reuse only the styled input border and dashboard cards. The input widget, viewport, menus, spinner, workers, header and live status still render from current state. The dashboard callback is read on every View. Keys cover rendered input, snapshot values, runtime counters, dimensions, language, styles and the palette renderer color environment; copied Models share a mutex-protected cache without retaining a Model or widget.

Paired controlled Model.View comparison with dashboard: median 253.6 → 122.4 µs and 938 → 158 allocations per call. In the real Bubble Tea producer/consumer path, 400 packets plus Done and 426–427 View calls were preserved; median summed View time was 77.1 → 25.1 ms. Output went to io.Discard, so this is repeated rendering work rather than a physical terminal, network or model latency measurement.

Parity with a cache-disabled fresh view passed for resize, keyboard/mouse scroll, draft/cursor/focus, copied Models, helpers outside Update, menus, workers, spinner, notices, changing dashboard/status callbacks, language/context, styles and renderer profile/background. Full TUI tests and Linux/macOS test cross-compilation passed.

## Native reasoning token estimation

The estimator now counts the existing byte slice directly instead of allocating a string copy of each native reasoning payload. Pi and Codex's local harness code provided a useful comparison for direct counting of existing blocks; SuperCli's exact estimator calibration stays the same.

Measured actual preparation sequence: two EstimateNextRequestTokens calls, buildToolDefs, and prepareProviderMessages(true), 12 blocks without reported Tokens, approximately 143k tokens:

| Case | Median preparation time | Allocated bytes |
| --- | ---: | ---: |
| Hidden historical messages | 325.8 → 125.1 µs | 1,224,826 → 45,137 B/op |
| Dormant earlier image | 118.5 → 54.2 µs | 431,881 → 38,649 B/op |

Prepared-history SHA-256, payload size, and estimated token count match before and after. The ordinary cached-history path retains the same allocations. These are local preparation savings, not a provider throughput or prompt-token reduction. Full llm and agent test packages passed.

## Narrow dead-code cleanup

Removed internal/tools/sandbox/helpers_windows.go after checking its private symlinkLink wrapper had no callers in the package AST across platform variants/tests and no source/build/documentation references. Removed the test-only applyVerification alias, an unused context import and two dummy references in internal/tools/core/verifier.go; the three existing tests call ApplyVerification directly. Net result: one deleted source file and 27 fewer lines. The actively used memory storage wrappers were retained. Full core, sandbox and agent tests passed on Windows, plus Linux test cross-compilation.

## Installed build and validation

Combined UI suite: 86/86. Full webgui, llm, agent, TUI, tools/core and tools/sandbox package tests passed in the relevant audit runs. Native GUI browser/link test: 17 checks; incremental render fixture: 190 prefix/recovery checks. Windows shell ABI tests ran successfully at 32 and 64 bits. Final Windows GUI/TUI and Linux/macOS GUI builds succeeded; Linux/macOS TUI cross-compilation passed.

Both root Windows EXEs were replaced with the final build and verified by SHA-256, without terminating any running application. Previous EXEs and local fixture outputs are kept under .tmp/browser-launch-fix-2026-10-01. Version remains 1.0.1; no commit, push or release was performed. These changes add no model request, prompt instruction or provider path; OpenCode Zen routing remains unchanged.
