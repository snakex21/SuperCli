# TUI line-width reuse — 2026-10-03

Parent baseline: 306ff569c4c6df05e667e9564d4e5ffa098b82d6 (dev13). All fixtures below are generated, not user conversations. No provider, model, cache setting or application was changed.

## Evidence and implementation

A bounded CPU profile of the existing 2,400-message history append benchmark measured 6.393 ms/op. The cumulative profile attributed 67.12% of sampled CPU to the pinned viewport's full-history terminal-width scan. Sampling includes benchmark setup; this is not an exact per-operation percentage or a model-generation result.

The local viewport derives from Bubbles v1.0.0. It keeps rendering, public scrolling methods, keymap and renderer commands identical to the pinned implementation. Only SetContent reuses widths for lines that equal the previous immutable normalized source at the same position. Each update owns a fresh two-byte-per-line metadata slice. A 65,535-column sentinel always recalculates the full terminal width; large content is not clipped by the cache.

The initial prototype compared mutable public line slices and was rejected after a real alias reproduction. The final implementation compares the immutable normalized source instead. Copied models, externally changed returned line slices, reset, changed prefixes, shrinking histories, CRLF and trailing empty lines are covered. Exact Unicode/ANSI calculation remains the upstream fallback.

## Final bounded comparison

Windows amd64, Go 1.26.2, Ryzen 7 5800X3D, generated mixed history, width 100 / height 36, NoColor. Serial A/B/B/A, 20 iterations per case. The baseline overlay restores the exact pinned viewport source in the new local package; the final candidate uses the tracked source. This includes completed-history refresh and View, not just the width scan.

| Messages | Pinned viewport | Final cache | Baseline bytes/op | Candidate bytes/op |
| --- | --- | --- | --- | --- |
| 60 | 0.424–0.554 ms | 0.287–0.339 ms | 203,582–206,853 | 208,262–208,523 |
| 600 | 2.142–2.312 ms | 0.588–0.601 ms | 868,587–872,351 | 875,747–875,868 |
| 2,400 | 7.672–7.932 ms | 1.646–2.246 ms | 3,014,922–3,015,049 | 3,068,951–3,072,406 |

An earlier isolated safe prototype comparison measured 6.843–6.957 ms versus 1.506–1.695 ms at 2,400 messages. Final measurements above are the integration evidence; machine load and bounded sample variance are visible.

This is a CPU/latency improvement with a small metadata cost, not a RAM reduction: approximately 54–57 KB additional allocation per append in the largest final fixture, and approximately two bytes retained per current line. Normalized content is retained by a string header referencing existing content bytes, not a duplicated full-history byte buffer. Reset replaces the old header and metadata. No model tokens, native decoding speed, whole-process RSS or end-to-end terminal paint improvement is inferred from this microbenchmark.

## Compatibility and maintenance

Direct differential tests use the actual pinned upstream dependency as the oracle, covering rendering bytes, public state and returned lines, exact widths around the sentinel, copies/aliases/reset, keyboard/mouse, horizontal scrolling and high-performance renderer command messages. Unsupported style frames larger than viewport dimensions are outside these generated renderer fixtures. Scoped viewport/TUI tests and vet pass; final global checks are recorded in the integration report.

An independent review verified that the remaining viewport implementation and keymap are byte-identical to the pinned upstream after removing the provenance header, cache fields and SetContent replacement. The component MIT license and copyright are preserved, including attribution in the root distribution license. No dependency or go.mod/go.sum change is introduced. Future Bubbles upgrades require deliberately refreshing this component and rerunning the oracle tests.

Portable ignored receipts: .tmp/goal-tui-round11-2026-10-03, including final A1/B1/B2/A2-local-safe-bench.txt, CPU profiles, local-viewport-tests.txt and local-viewport-vet.txt. Only redacted aggregate evidence is committed.
