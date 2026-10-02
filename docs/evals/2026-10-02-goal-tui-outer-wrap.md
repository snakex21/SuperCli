# TUI completed-history outer-wrap reduction — 2026-10-02

## Change and boundary

After a completed row is appended, `renderCompleted` rebuilds completed history. Ordinary user/assistant bodies were wrapped inside `renderRoleBlock` and then wrapped again as a complete row. Completed rendering now derives a private fit hint from the already rendered label/body and actual gutter. It skips only the outer `ansi.Wrap` for printable ASCII, LF and complete numeric SGR, a fitting label, width above three, and the standard one-cell gutter. No styles are rendered twice; callback order is unchanged. No Model fields, persistent cache, prefix reuse, pointer ownership, additional timer or provider calls were added. Active streaming and other roles retain their existing paths.

Unicode, other terminal controls, malformed ANSI, narrow widths, wide labels and transformed/framed/sized gutters use the unchanged legacy `renderRoleBlock` and outer wrap. A broader Unicode hint was rejected: combining marks plus NBSP at width five changed bytes even when display widths looked sufficient. This exact counterexample has a fixed golden regression. A second mixed Unicode/control counterexample is covered by seeded replay. Stateful style transforms are compared for call order/count as well as output. All 27 language labels are checked.

## Matched-source evidence

The generated history shape is bounded by read-only metadata: the largest saved session has 2475 messages, predominantly tools and assistant text; average tool content is 2288 characters and assistant text parts average 2022. The surrogate contains no private conversation text. Width 100, NoColor palette, 60/600/2400 mixed rows, one completed tool append. Initial seed render and independent message-slice copy are outside the timer. Windows/amd64, Ryzen 7 5800X3D, GOMAXPROCS=2. Final A-B-B-A runs use the same benchmark and current remaining source; baseline overlays only the original `view_chat.go`. Ten iterations per case:

| Rows | Before time | After time | Before allocated bytes/op | After allocated bytes/op | Before/after allocations |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 60 | 3.64–3.81 ms | 3.27–3.37 ms | 1.454–1.458 MB | 1.316–1.320 MB | 8598–8600 / 8393–8394 |
| 600 | 36.93–37.30 ms | 32.71–32.81 ms | 15.907–15.914 MB | 14.483–14.487 MB | 86717–86718 / 84630–84631 |
| 2400 | 144.10–145.48 ms | 125.39–127.97 ms | 63.807–63.815 MB | 58.156–58.159 MB | 346471–346477 / 338157 |

At 2400 rows this is about 12.5% less time and 5.65 MB less temporary allocation per completed append. It is not retained-RAM/RSS reduction, keystroke latency, terminal paint or model-generation speed. Unchanged completed-cache calls remain zero allocations. Final same-process row A-B-B-A fallback checks show unchanged allocation counts and no material consistent regression for Unicode, controls or custom wide gutters. Eligible ASCII rows take roughly 138→117 µs plain and 169→139 µs colored. Unicode conversations do not receive this shortcut.

Validation: `go test ./internal/ui/tui -count=1` and `go vet ./internal/ui/tui` pass. Byte parity includes Unicode/combining/CJK/emoji, malformed UTF-8/ANSI, OSC, CR/tab/control cases, widths 0–100, folded reasoning, custom geometry, colored styles and stateful callbacks. The durable benchmark isolates append redraw rather than idle history-cache behavior.

## GUI audit and limits

Two current-source isolated native WebView2 fixtures loaded 60/600/2400 generated rows through existing 60-row incremental pages, then streamed a generated answer. Incoming older-page preparation/prepend/geometry stays at most 5.4 ms; both fixtures have exact final DOM, no stream long tasks and no frames above 20 ms. Full 2400-row DOM has 27690 elements and raises own process-tree private working set by about 83.6–83.7 MiB. This load was explicit; no current GUI defect was reproduced. Browser full GC was unavailable, so post-reset heap/RSS observations do not establish a leak or retention fix. No user app, images or live provider were exercised.

Detailed evidence: `.tmp/goal-ui-history-2026-10-02-round5/REPORT.md`, `wrap-final-abba-bench.json`, `wrap-matched-gutter-guard.json`, `wrap-production-tui-result.json` and `wrap-production-vet-result.json`. Native results are `history-first.json` and `after.json`; generated source and private profiles remain inside the artifact directory. The earlier alias-unsafe completed-prefix cache and broad Unicode wrap hint remain rejected.
