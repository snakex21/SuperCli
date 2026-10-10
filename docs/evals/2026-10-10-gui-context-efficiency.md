# GUI context visibility and report efficiency — 2026-10-10

The stats sidebar now keeps a context row even when no usage snapshot or backend
window has been recorded. Its limit uses the same scoped override, configured
window, capability/unique alias, learned limit and local/remote fallback policy
as the agent loop. The model and user-selected thinking setting are unchanged.

## Context visibility

Previously, the sidebar omitted context whenever persisted `context_window` was
zero. The usage recorder resolved only configuration and exact catalog IDs,
whereas the loop also used manual model overrides, aliases, learned limits and
fallbacks. Unknown local models could therefore have a usable loop budget and
an empty sidebar meter at the same time.

The recorder and stats reader now use the shared resolution cascade. A new chat
has a window but explicitly has no usage snapshot; the UI displays `— / limit`
with an explanatory tooltip rather than inventing 0% usage. An unknown window
also keeps a visible row. Limits known only through fallback resolution receive
a `~` marker. No model call, provider rescan or new loop is created to obtain it.

Each usage row now retains its purpose in the existing `source` field.
`model`, `main`, `legacy` and empty sources remain compatible main-call records.
Compact, title, worker and other helper calls continue to contribute to every
token and cost total, but cannot replace the main context snapshot or its model
identity. Historical records without a window resolve their own model/provider,
including separate profiles on the same endpoint; they cannot borrow another
profile's manual override.

Same-session refresh builds a replacement off the visible DOM and keeps the
previous meter until the response completes. Changing sessions clears that
meter immediately and displays an unknown-context placeholder. Sequence and
session checks reject stale successes, failures and configuration responses.
This adds no refresh timer or network request.

## Local report optimization

`ContextReport` previously generated labels for every visible message/schema
and stable-sorted the entire list, although it displayed only five items.
Each label's `firstWords` normalized the whole message, including large tool
results. The new version retains five stable descriptors, creates only their
labels, and scans only the Unicode prefix needed for the eight-word excerpt.

The full report and formatted report remain byte-for-byte compatible with an
independent preserved legacy implementation. Tests cover ties, zero/small lists,
Unicode whitespace, invalid UTF-8, large and negative word limits, randomized
text, hidden messages, four routes, thin schemas and late registry changes.
Counting, provider calibration, canonical history and visibility stay intact.
There is no new cache or mutation of agent state.

Windows amd64, Ryzen 7 5800X3D. Fixture: 127 messages, short versus large tool
results, three 200 ms benchmark samples per variant. Medians of complete local
report construction:

| Fixture | Before | After | Temporary allocated bytes before → after |
|---|---:|---:|---:|
| Full schemas, short results | 0.296 ms | 0.012 ms | 212,127 → 6,486 B |
| Full schemas, large results | 42.731 ms | 0.361 ms | 68,638,698 → 6,480 B |
| Thin schemas, short results | 0.370 ms | 0.023 ms | 221,494 → 15,979 B |
| Thin schemas, large results | 68.748 ms | 0.353 ms | 68,648,410 → 16,184 B |

The large full-schema fixture changed from 2350 to 46 allocations per report.
This is a bounded local-report improvement (roughly 118× for that fixture),
not a measurement of model generation, compaction inference, total application
or model RAM, tool-call count, or whole-task wall time. Ordinary short reports
save fractions of a millisecond. No live inference ran in this change.

## Verification and limits

- Full agent, session-storage, application, TUI and GUI test suites passed;
  vet passed for all five packages.
- GUI tests were repeated after the final provider-scope correction.
- All 255 browser-side tests passed, including real DOM context rendering,
  missing data, pending refresh, session switches and stale responses.
- Both command binaries built successfully.
- Static review checked provider/window isolation, billing totals, cancellation,
  sequence guards and legacy report equivalence.

The sidebar describes the last registered main request. It does not become an
exact new model-visible snapshot after manual compaction followed by reload.
The compaction response still contains its fresh inspector report. Older compact
rows incorrectly saved as `model` remain historical data; no heuristic migration
or new table was introduced. Legacy conversations without a main request retain
their streamed transcript estimate, including its existing limitations.

Evidence is portable under `.tmp/gui-context-efficiency-fix/`: check logs and
`checks.json`, `webgui-final-frozen.log`, `ui-tests-final.log`, benchmark samples and
`benchmark-receipt.json`. The preserved pre-change report source is
`.tmp/ce/context_report_before.go`. No app data was redirected to a system or
user-profile directory.

The final embedded GUI assets passed all 255 UI tests again before rebuilding.
Both installed binaries match their staged hashes and passed installed `--help`
smoke checks. `installed.json`, `installed-smoke.json` and `installed-sources.json`
retain the installation/source evidence. Previous binaries are recoverable under
`.tmp/gui-context-efficiency-fix/backups/7a0ad9b8-fdac-48e3-b8df-c4319005efca/`.
Reopening CLI/GUI loads the changes; the running application was not restarted.
