# User facts before GUI response headers — 2026-10-09

A pure fact extraction guard now bypasses acquiring/opening the global memory store when a prompt has no durable user facts. The normal saver, factual persistence, deduplication, retry-after-open-failure and later project briefing remain in place. This removes only the pre-header write path: later briefing still reads persisted global/project history and can open the database.

Paired overlay benchmark: Windows amd64, Ryzen 7 5800X3D, GOMAXPROCS=2, ten operations per sample, three samples. Medians for this helper only:

| Case | Before | After | Store opens/op before → after |
|---|---:|---:|---:|
| Fresh store, no facts | 11.065 ms | 2.34 µs | 1 → 0 |
| Existing unopened store, no facts | 4.233 ms | 1.44 µs | 1 → 0 |
| Already open store, no facts | 2.77 µs | 1.30 µs | 0 → 0 |
| Fresh store, real facts | 24.946 ms | 25.209 ms | 1 → 1 |
| Existing unopened store, real facts | 6.647 ms | 6.723 ms | 1 → 1 |
| Already open store, real facts | 116.73 µs | 119.62 µs | 0 → 0 |

The guard performs a second small extraction for real declarations: warm factual calls allocate 7,280 rather than 5,968 bytes (193 rather than 164 allocations). Short samples do not establish a significant timing change for real facts. No claim of eliminating all database opens, reducing WebView RSS, model tokens or pure provider prefill time follows from these measurements.

Six top-level regressions cover no-fact prompts, a held store mutex, actual HTTP headers with a cold memory store followed by history briefing, Polish/English/Unicode saver parity and durable deduplication, stored global/project history, and failed-open retry. An existing ASCII-token filter rejecting a particular short Unicode value is preserved and documented, not fixed in this batch.

Evidence: .tmp/optimization-oct8-2026/go-user-facts-before-oct9-p-0.log, go-user-facts-after-oct9-p-0.log, go-gui-console-fix-oct9-p-0.log; raw production preimage and baseline overlay under closure-audit/user-facts-before-p.json and user-facts-original-p.overlay.json.
