# GUI diff summary: bounded temporary memory

The folded tool summary previously split the entire diff result into a line
array and tested each resulting substring with two regular expressions.
`toolChangeStats` runs when restored history is shown and when a live tool
finishes, before the lazy diff viewer opens. Large diffs therefore paid this
temporary allocation even while their details stayed folded.

The helper now inspects only the start of each LF-delimited line and locates
the next line with `indexOf`. It creates no line array or line substrings. The
full stored result, expanded viewer, summary counts and visible UI are
preserved. In particular, the existing three-minus header counting behavior
is intentionally preserved; this patch changes resource use only.

## Measurement

Windows, Node v24.15.0, 2026-10-08. Run:

```text
node --expose-gc scripts\bench-tool-change-stats.cjs
```

The fixture is 4,600,000 ASCII characters, with 240,000 newline-terminated
lines. Each implementation is warmed three times and measured seven times,
with an explicit GC before each measured call. Both return exactly
`{added: 40000, removed: 80000, diff: true}`.

| Metric (median of 7) | Original split/regex helper | Line-start scanner |
| --- | ---: | ---: |
| Elapsed time | 14.3631 ms | 2.8788 ms |
| Heap used after call minus heap used before call | 42,565,784 bytes | 584 bytes |

The helper was about 5 times faster in this fixture. The old implementation
left about 40.6 MiB of temporary allocations awaiting GC, whereas the scanner
left less than 1 KiB at the median. Heap deltas are noisy at such small sizes
(one optimized run was negative because runtime bookkeeping was collected).
This measures the summary helper in Node; it does not measure persistent app
RAM, WebView2 RSS, layout, rendering or FPS. The benchmark uses synthetic
results and does not read or modify user sessions.

## Regression coverage

```text
node --test test\ui\tool-change-stats.test.cjs test\ui\history-tool-lazy.test.cjs test\ui\tool-media-preview.test.cjs
```

43 tests passed. The new checks cover all six diff tool names, LF/CRLF/bare-CR
boundaries, prefix edge cases, Unicode, primitive/coerced input, 1,500
deterministic randomized parity cases, and a 300,000-line result whose string
`split` method is disabled. Existing lazy history, live completion, expanded
payload, media preview and paging regressions passed in the same run.

No Go tests or builds were started by this subtask.
