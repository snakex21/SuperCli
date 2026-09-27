# Bounded worker report outline — 2026-09-26

## Observed loss

A completed review from the fixed GunMayhem session snapshot contained a 16,598-byte final report, including nine numbered findings. The coordinator's generic first/last-byte preview retained the verdict, a lengthy flow description and final verification status, but none of the nine finding titles in the middle.

The report was recovered from the already completed stored output. No active session was polled, no provider was called, and no game files were modified.

## Change

Successful structured worker reports that exceed the existing inline threshold now use Result.ModelPreview. Within the same 4,096-byte preview budget, the parent receives:

- A bounded outline of original Markdown ATX headings, excluding fenced/indented code.
- A beginning/end excerpt of the report, with explicit omission markers.
- The worker identity/status and existing inline observation or attachment metadata.

The full UI report and retained report/tool observations are unchanged and remain retrievable through read_output. Short reports, plain reports with fewer than three headings, and failed worker results keep their existing behavior. A bounded outline buffer and UTF-8-safe cuts handle oversized headings/reports. This runs in the common worker result path for local/cloud and CLI/TUI/GUI; no provider routing, Zen transport, schema or model prompt was changed.

## Replay

The opt-in test replays the saved final report using a representative worker notification and the real registry output store. Sizes include the output retrieval handle; they are bytes, not token estimates.

| Measurement | Before | After |
| --- | ---: | ---: |
| Numbered finding titles visible | 0 of 9 | 9 of 9 |
| Model-visible preview bytes | 4,324 | 4,144 |
| Additional model calls | 0 | 0 |

The verdict and final statement that tests could not be run remain visible. The outline provides navigation, not the omitted evidence; the parent can still retrieve detailed findings when needed. This demonstrates improved evidence coverage at a slightly lower byte cost, not a measured end-to-end provider latency improvement or a guaranteed elimination of a tool call.

Private replay artifacts are kept under .tmp/worker-report-2026-09-26/, not committed. Set SUPERCLI_TEST_WORKER_REPORT to a saved report and run TestWorkerReportPreviewReplay to reproduce its coverage comparison.

## Validation

- Regression was red before the change: three synthetic findings hidden in the middle were lost.
- Focused tests pass: finding preservation, full read_output retrieval, unchanged UI text, inline/attached observations, error cause preservation, small/plain fallback, fenced-code exclusion, Unicode and thousands of headings.
- Saved-report replay passes with all nine numbered findings visible.
- Full go test ./... and go vet ./... pass.
- Both Windows binaries rebuilt and installed with previous versions backed up under the replay artifact directory; CLI --help smoke check passed. Restart running instances to use the new build.

## Follow-up

The [opt-in synthetic local/cloud replay](2026-09-26-worker-report-live.md) now tests coordinator behavior. It records both reductions and regressions across routes; fewer visible omissions do not guarantee lower wall time.
