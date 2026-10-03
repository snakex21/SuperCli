# TUI canonical transcript ownership

The private raw transcript mirror served legacy test assertions and one welcome/resize emptiness check. Rendering, search, clipboard source and resumed tool/reasoning history already use canonical chat messages. Production now retains only a presence flag with the same empty-line and reset behavior. Legacy tests inspect canonical messages on demand in a test-only helper.

Frozen baseline ad72f73 versus isolated candidate: full TUI suites passed, and exact public rendering/search/copy snapshots matched (42,601 bytes, SHA256 192a90a493d02915932b043ec51beac0fa0fc636c39d74f64d24a6c7bf0796ec). Generated histories removed 704,512 bytes of redundant retained capacity at 2,400 short records and 2,588,672 bytes at 12 large records. An 8x ABBA resume benchmark reduced allocations at 2,400 records from 26.39–26.43 MB to 23.15–23.17 MB per operation. Timing samples overlap; these measurements do not establish FPS, process RSS or WebView2 savings.

Production regressions cover session replacement, Unicode/tool/native reasoning preservation through existing tests, resize, search, exact copied answers, copied model isolation, welcome restoration and empty-line presence. Integrated TUI suite passed after porting. Ignored reproducible fixtures/results: .tmp/goal-ui-dev22-2026-10-03 and .tmp/codex-accounts-dev23-2026-10-03/tui-proof.txt.

# Deferred context-read experiment

An isolated direct SQL-row decode removed about 1.9 MB allocation at 4,096 rows but repeated full-history timings regressed (baseline 20.00–20.22 ms, candidate 20.96–23.15 ms). A bounded 64-row decode reduced allocation by approximately 2.42 MB and 4,105 allocations, with better large-history samples but inconsistent small/large-payload timings (8 x 512 KiB baseline 3.91–4.09 ms, candidate 4.13–4.53 ms). Both passed session snapshot/corruption tests. Neither storage prototype is adopted because the requested speed improvement is not consistently established. Existing transactional snapshot and row clearing remain. Experiments: .tmp/goal-context-row-stream-2026-10-03.
