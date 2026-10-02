# TUI active transcript assembly — 2026-10-02, round 6

## Change

renderWithSpinner now renders its completed prefix and active section in the existing order, then reserves the exact combined byte length once before assembling them. Previously the first Builder.WriteString allocated the completed prefix; appending an active block beyond that allocation's slack grew the buffer and copied the entire prefix again. This happened on ordinary changed spinner frames as well as active text updates.

The current/separator decisions remain at their original points relative to style callbacks. The exact text, role spacing, spinner placement, completed cache, active cache cleanup and idle early return are preserved. There is no new persistent cache, Model field, style/prefix reuse, timer, option, dependency or provider call. The previous completed outer-wrap change remains separate.

## Matched-source measurements

Base 9fab896, Windows amd64 / Ryzen 7 5800X3D, Go 1.26.2, GOMAXPROCS=2. A-B-B-A, 100 iterations per sample. Both ignored overlays share the same generated fixture; candidate replaces only renderWithSpinner. Histories use the tracked 60/600/2400 mixed-message fixture, bounded by earlier aggregate saved-session metadata (largest 2475 messages, maximum assistant text part 58493 characters), without reading or replaying private content. NoColor, width 100, height 36; initialization and cached completed rendering are outside timing.

The measured real refreshTranscript includes resize/follow logic and viewport.SetContent. Spinner frames alternate spin-A/spin-B, so the unchanged-content setter cannot bypass the changed viewport. Commands returned by the spinner are never run. Dashboard callbacks, Tea event boxing and physical terminal painting are excluded.

| History / active source | Baseline full refresh | Final behavior | Baseline B/op | After B/op | Allocations |
| --- | ---: | ---: | ---: | ---: | ---: |
| 600 / 4 KiB | 1.669–1.727 ms | 1.550–1.561 ms | 1399056 | 694544 | 12→11 |
| 2400 / 4 KiB | 6.391–6.721 ms | 6.152–6.502 ms | 2717968–2717969 | 2717968 | 11→11 |
| 2400 / 32 KiB | 6.494–6.550 ms | 6.301–6.355 ms | 5544208 | 2758929 | 12→11 |

The conditional savings are 704512 B/frame at 600 / 4 KiB and 2785279 B/frame at 2400 / 32 KiB. At 2400 / 4 KiB the old buffer already has enough slack, so allocations are unchanged and timing overlaps. Merge-only 20x samples at 2400 / 32 KiB measured 0.554–0.606→0.269–0.336 ms, but the complete refresh improves only about 3% because Bubbles still scans historical lines. Smaller/empty cases keep the same allocation count/bytes; their short timings are noisy and no speedup is claimed.

The tracked port differs from the measured candidate only by explanatory comments. The lasting BenchmarkTUIActiveHistoryAssembly uses the same completed-history fixture and changed-spinner refresh path, including no-history and no-saving controls. Benchmarks were not rerun after a byte-equivalent port solely to produce more samples.

## Validation

An ignored comparison of the old function with the candidate passed 675 cases: Unicode/Markdown/current states, narrow widths, plain/color/custom/stateful styles, spinner presence, exact output, callback order/count and active-cache cleanup. The permanent tests keep finite expected bytes, a large immutable prefix/copy/flush case, and an explicit callback mutation case proving completed-before-active order and separator placement. They do not embed a duplicate renderer. Existing active-section tests cover reasoning/tool/error/done boundaries, copied chats, concurrent cache access, fresh rendering, colors and folds.

Production whole go test ./internal/ui/tui -count=1 passes (3.392 s); go vet ./internal/ui/tui passes. Scoped formatting and diff checks are clean. No commit, staging or user EXE replacement was performed by this agent.

## Limits

B/op is temporary cumulative allocation, not retained RAM/RSS. No new native GUI, terminal frame or provider measurement was made. The change does not reduce model request tokens, prompt processing, provider TTFT, agent turns or GUI latency. Provider-prefill investigation remains independent and the higher user priority. The alias-unsafe completed-prefix proposal remains rejected.

Evidence: .tmp/goal-ui-round6-2026-10-02/REPORT.md; changed-spinner-refresh-abba.json; active-merge-abba-bench.json; parity-result.json; production-checks.json; baseline/candidate overlays and source hashes. All artifact/cache/temp writes remain in repository .tmp.
