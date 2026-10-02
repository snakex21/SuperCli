# TUI viewport/search and completed-prefix audit — 2026-10-02

Base: main `cb7f507` (clean at start). Portable experiments in .tmp/tui-completed-prefix-audit-2026-10-02. Windows amd64, Ryzen 7 5800X3D, GOMAXPROCS=2. No provider/model requests, GPU workloads, user applications, or package installations.

## Integrated search fixes

1. In an idle conversation, Ctrl+F → find a multiline answer → Space → Esc marked the message folded, but the viewport still showed its hidden second line. The menu close path now refreshes only when the completed transcript is dirty and there are completed messages or active text. A dirty empty chat retains its welcome view, including after reasoning-setting confirmation. Ctrl+K closing the menu uses the same behavior. Refresh keeps the existing follow-tail/manual-scroll semantics.
2. At width 30, an expanded read_file result containing 330 characters wrapped into multiple terminal lines. renderedLineForMessage(1) returned row 2 while the target system message was actually at row 17. Search positioning now uses the same outer ANSI wrapping as the completed transcript. It also includes the separator immediately before a target user/assistant block.

The complete internal/ui/tui test package passed (1.889 s), including reasoning confirmation. Focused regressions cover idle folding with Esc/Ctrl+K, return through a parent menu, dirty-empty welcome preservation, retained manual scroll, folded/expanded tool outputs, command documents, folded individual messages and reasoning, Unicode, ANSI/TrueColor, and first/negative/past-end indices. Only menu.go, renderedLineForMessage in view_chat.go, and the new transcript_search_refresh_test.go were changed for these fixes. The source fields/logic of the experimental completed-prefix cache were not integrated.

## Rejected completed-prefix experiment

The existing cache avoids rerendering completed history on ordinary provider text deltas. Adding a completed message marks it dirty and rebuilds all old messages. The isolated candidate retained one rendered prefix plus a rendered-message count, rendered only appended messages, and rebuilt for width/language/folding/tool-expansion changes and history edits.

Ordinary byte-for-byte parity passed after 500 mixed user/assistant/tool/system/document messages followed by 100 appended events, at multiple widths, in all 27 languages, with folded/expanded reasoning/tool/message states, removed transient messages, and both NoColor and TrueColor. Presentation-only copied chats also passed.

The candidate was rejected because stronger parity tests failed:

- A copied chat folded an earlier message through the shared msgs backing array. After the original chat appended a new event, its retained prefix hid the changed earlier message; the full renderer showed it.
- Two copied chats appended through shared spare slice capacity. A later append reused cached text from a message overwritten by the other copy.
- A stateful Style.Transform callback changed its output before an append. The existing full rebuild applied its current output to previous messages, while the candidate retained old styling.

No prefix optimization was committed to tracked sources. Resolving history ownership/invalidation and style callback behavior would add work or retained metadata; that cost has not been measured.

### Upper-bound measurements, not a shipped improvement

A-B-B-A process order, two benchmark samples per process, 100 ms requested benchtime. Each operation is a complete batch of 100 appends after a warmed 500-message mixed history; initialization is outside the measured region. The baseline batch exceeds the requested duration, so each baseline sample ran one batch. Reported times and allocated bytes are medians of four samples. Both source variants include the same test-only renderMsg counter.

| Per 100-append batch | Existing full rebuild | Rejected prefix candidate |
| --- | ---: | ---: |
| renderCompleted time | 1.319 s | 2.125 ms |
| renderCompleted allocated bytes | ~221.85 MB | ~9.82 MB |
| refreshTranscript time | 1.381 s | 54.87 ms |
| refreshTranscript allocated bytes | ~240.41 MB | ~27.75 MB |
| renderMsg calls per append | 550.5 | 1 |

refreshTranscript runs the real model/viewport path with unchanged active streaming text and the spinner. Its remaining work includes combining the full transcript and Bubbles SetContent splitting/scanning all lines. Bytes/op are cumulative allocations per batch, not retained heap, RSS, or WebView2 RAM. The prefix numbers are an unsafe upper-bound experiment and must not be described as achieved application performance.

Artifacts retain baseline/candidate overlays, parity failures, A-B-B-A benchmark outputs, and the focused search-fix test result. No background daemon, timer, UI option, model instruction, or polling was added.

## Safe unchanged-content reuse

The separately integrated viewport_content.go setter records the already supplied viewport content. When the exact string is unchanged, refreshTranscript does not repeat Bubbles SetContent, including its complete line split and longest-line-width scan. Welcome, language, and ordinary refresh writers use the same setter. With no current assistant text and no spinner, renderWithSpinner returns the existing completed cache directly after active-section cleanup, avoiding an identical full-history builder copy.

Fresh matched-source A-B-B-A measurement supplied by the integrating agent: 100 repeated refresh calls per benchmark sample after warmup. These are unchanged-content refreshes, not the 100 changing appends in the rejected prefix experiment above. Two samples for each version; table times are their medians.

| Completed history | Before | After | Allocated bytes before → after | Allocations before → after |
| --- | ---: | ---: | ---: | ---: |
| 500 mixed messages | 282.493 µs/refresh | 7.630 µs/refresh | 98,304–98,305 → 0 B/op | 2 → 0 |
| 50 messages | 35.347 µs/refresh | 9.237 µs/refresh | 9,472 → 0 B/op | 2 → 0 |
| Empty history | 7.576 µs/refresh | 7.934 µs/refresh | 16 → 0 B/op | 1 → 0 |

There is no claimed empty-history time improvement. The preserved parity cases include width changes, manual top position, folding, current text, welcome views, copied models, and the search fixes. This optimization keeps the existing completedDirty behavior and does not reuse a stale completed prefix after changing appends.

Model sizeof changes from 46,056 to 46,080 bytes (+24 bytes of fields/alignment). For LF content, stored string headers refer to the same content underlying viewport storage rather than making a deep copy. Bubbles creates a normalized copy for CRLF input, so that input bypasses cache-key retention and keeps the original SetContent path; TestViewportContentDoesNotRetainRawCRLFKey covers the return to LF reuse. The zero allocation figures describe repeated unchanged-content operations; they do not describe first rendering, changing content, process RSS, or WebView2 memory.

Final post-CRLF-guard measurement artifact: .tmp/goal-round-three-2026-10-02/viewport-final-valid-bench.json. The earlier viewport-bench.json is retained as pre-guard evidence. The helper and its regression coverage are viewport_content.go / viewport_content_test.go; no extra timer, daemon, option, or model prompt was introduced.

Changed-content control: a separate ABBA 100x benchmark runs active appends and same-length replacements after 0/500 messages. Allocation counts remain 145/136 for both versions, with overlapping timing samples and no clear improvement or material regression. The equality guard therefore is not described as accelerating changed frames. Raw results: .tmp/goal-round-three-2026-10-02/viewport-changing-bench.json.
