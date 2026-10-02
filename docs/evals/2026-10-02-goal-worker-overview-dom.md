# Worker overview DOM placement — 2026-10-02

Base: 2b4c132 (dev.11). Every worker started/tool-call/tool-result/finished update sorted the overview and re-appended every existing button, even when the sorted order was unchanged. Labels and state needed refreshing, but those repeated node moves did not.

The overview now compares each existing button with the child at its sorted position and calls insertBefore only when they differ. Sorting, labels, status, activity, summary and the button callback remain unchanged. There is no retained cache, timer, dependency, provider instruction or extra backend work.

An isolated DOM double runs the production addToolCall/addWorkerProgress functions with generated task briefs, 20 tool-call/result pairs per worker, language refreshes and final statuses. Workers are created in reverse numeric order so initial relocation remains covered:

| Workers | Overview events | Existing-button moves before | After | Newly added buttons before/after |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 45 | 45 | 0 | 1 / 1 |
| 4 | 171 | 678 | 3 | 4 / 4 |
| 16 | 675 | 10,680 | 15 | 16 / 16 |

Every event has identical order, displayed text, status/title/dataset and overview summary in the baseline and candidate. Button identities and the click callback opening the correct task row also match. The workload is bounded and generated; 16 is a fixture size, not a claim about the configured worker limit or the frequency in a saved conversation. A move counts a DOM insertion call on an already parented node, including a redundant re-append of the final or sole child.

Two permanent regressions cover numeric ordering, unchanged-order tool events, English/Polish status and summary, stable button identity, clicks after the task-row target changes, detached rows and reset. Both fail on the baseline and pass after the port; the two existing worker-steering regressions also pass. The entire 162-test UI suite passed against the exact candidate overlay (809.1 ms); the loader recorded 199 actual production-source reads. The final production source is byte-identical to that tested overlay, so the full suite was not repeated solely for the port. Its existing ordinary no-image/no-history stream, reasoning boundary, Markdown, scroll, media and history cases passed unchanged.

These are deterministic DOM-work and behavior measurements. They do not measure WebView2 RSS, browser heap, layout/paint cost, FPS, input latency, provider token throughput or actual delegation benefit. Raw worker outputs and transcript evidence remain available.

Private reproducible evidence: .tmp/goal-ui-round9-2026-10-02, including source snapshots, generated replay, baseline/port test receipts, overlay-loader interception receipt and final source hashes. Full application integration belongs to the main agent.
