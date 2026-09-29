# Live statistics ledger reuse — 2026-09-30

The GUI statistics endpoint previously opened the portable SQLite database, checked the shared schema, ran all four credit-ledger schema statements, read the daily total, and closed the connection on every refresh. The engine now initializes one ledger handle on its first statistics request and closes it at shutdown. Totals are still queried on every refresh, so writes made by another TUI/GUI process remain visible. Initialization failures close the temporary connection and can be retried.

## Measurement

Windows amd64, AMD Ryzen 7 5800X3D. Existing BenchmarkStatsLongSession, three repetitions per fixture, 300 ms per sample. Both versions verify the complete returned statistics against the same expected value on every iteration. These are synthetic dashboard measurements, not model response latency.

| Session fixture | Before, median | After, median | Time reduction | Allocations before / after |
| --- | ---: | ---: | ---: | ---: |
| 10 turns | 2.510 ms | 0.755 ms | 69.9% | 1433 / 1255 |
| 200 turns | 11.714 ms | 9.522 ms | 18.7% | 21313 / 21138 |

Per-refresh allocated bytes fell from approximately 77 KB to 69 KB for the smaller fixture and from 1.050 MB to 1.042 MB for the larger one. Longer-session work still includes counting transcript structure and reading recorded model calls.

Raw results are retained locally in .tmp/readme-tui-en/stats-before.txt and stats-after.txt. Reproduce with go test ./internal/webgui -run '^$' -bench '^BenchmarkStatsLongSession$' -benchmem -benchtime=300ms -count=3.

## Validation

Regression tests check new ledger writes through a second SQLite connection, separate web usage counted exactly once, previous-day records excluded, concurrent initialization returning one handle, canceled initialization with a successful retry, and shutdown closing the handle and preventing reopening. The webgui, account/credits and TUI suites pass, as does go vet for those packages. Focused webgui regressions also pass with the Go race detector, and the Windows GUI build succeeds.

The change is lazy and adds no startup database work. Interface images in the main README now use the English TUI action centre; the original Polish view remains in the screenshot gallery.
