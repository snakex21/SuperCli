# GUI/TUI residency and formatting — 2026-10-02

Marker now stores its eight used styles by value instead of embedding the full
Palette. This preserves zero-value rendering and independent reassignment of
styles in copied markers or their source palette. The main Model palette remains
by value. Session date groups reuse the existing bounded, locale-scoped formatter
cache, with separate keys for dates with and without a displayed year.

## Measurements

Windows amd64, Ryzen 5800X3D, GOMAXPROCS=2; three 500 ms benchmark repetitions.
Typing measures one rune followed by Backspace using the existing composer fixture.

| Fixture | Before | After |
| --- | ---: | ---: |
| Model size | 63,176 B | 46,056 B |
| Marker size | 21,552 B | 4,432 B |
| Typing allocations | 136,152 B / 107 allocations | 103,381–103,382 B / 107 allocations |
| Typing median | 54.43 µs | 44.79 µs |
| Tool-result render allocations | 1,320 B / 49 allocations | 1,320 B / 49 allocations |
| Tool-result render median | 10.09 µs | 8.82 µs |
| Date formatting, 40 older session rows | 2.396 ms | 0.165 ms |

Warm cached date formatting creates no further ICU formatters; the uncached
fixture creates one per row. The cache needs two entries per selected language.
A pointer-to-Palette prototype was rejected: tool-result rendering increased to
23,082 B and 50 allocations per render.

## Verification and limits

Full TUI tests and 133 GUI tests pass. Added regressions cover marker copy/source
palette isolation, zero-value markers, every supported locale, calendar grouping,
year rollover, locale changes and unavailable ICU retry/fallback.

These are synthetic preparation/formatting measurements, not physical terminal
input latency, WebView2 process working set or total application RAM. Typing
allocation count is unchanged; copied Model values remain boxed into tea.Model.
Unchanged session snapshots already skip sidebar redraws, so the date saving
applies when a redraw is needed. No timers, new options or model calls were added.

Portable experiment artifacts and reproducible overlays are under
.tmp/ui-audit-2026-10-02-round2/; the baseline marker source is retained there.
