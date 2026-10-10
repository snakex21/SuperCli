# TUI transcript presence without discarded formatting

The raw transcript mirror was previously replaced with `hasTranscript`, but its
legacy helper still accepted an ignored string. ToolResultEvent consequently
formatted a tool marker only to discard it, then formatted the visible marker
again during canonical chat rendering. Worker tool-call markers and some status
strings were also computed solely for the ignored argument.

All real call sites now use private `markTranscriptPresent()` with no parameter.
Only computations used exclusively by the ignored argument are removed. Canonical
chat appends, worker panel updates, visible Marker methods, reasoning, completion,
errors and restore remain on their existing paths. Empty lines, empty user/tool
records and empty command documents still establish transcript presence; an empty
restored conversation still clears it. No cache, provider request, routing,
portable data storage or transcript-search behavior changes.

New regressions exercise actual ToolResultEvent canonical content, matching tool
identity, pending-call cleanup, error counts, image references, folded/expanded
viewport bytes and top/bottom scroll behavior. They additionally cover worker
tool-call presence without adding an invisible chat line, empty completion,
reasoning/error cleanup, empty restored records and command documents. Existing
empty-line/resize, search/copy and session-replacement regressions remain.

An isolated A/B fixture executes the real compiled ToolResultEvent handler using
synthetic raw/JSON results at approximately 512 B, 128 KiB and 1 MiB. The baseline
overlay uses the exact preserved pre-change sources; the candidate uses production.
Both run the same benchmark and exact state/viewport snapshot trace. The baseline
copies are confined to `.tmp/optimization-oct8-2026/tui-transcript-presence-experiment`.
Permanent tests contain no second implementation of the handler.

The central baseline and candidate regression runs passed. Their exact snapshot
receipts match: 192 states, 3,154,531 bytes and SHA256
`f974e47a89b096e5e3905021e8f2a7d0e06af29d78fc88d3fc0223d0723db8a1`.
The trace includes English/Polish, folded/expanded, top/bottom scroll, errors,
images, workers, reasoning, completion, notices, restore and welcome resize.

Three 300 ms samples per case gave these medians for the complete handler on the
local Windows host (two Go scheduler threads):

| Payload and view | Before | After | Before bytes/op | After bytes/op |
| --- | ---: | ---: | ---: | ---: |
| 512 B raw, folded | 121 us | 100 us | 140,195 | 135,483 |
| 512 B JSON, folded | 133 us | 106 us | 145,588 | 138,115 |
| 128 KiB raw, folded | 956 us | 909 us | 653,362 | 648,574 |
| 128 KiB JSON, folded | 2.368 ms | 1.544 ms | 1,735,429 | 1,173,232 |
| 1 MiB raw, folded | 5.862 ms | 4.869 ms | 5,380,488 | 5,375,663 |
| 1 MiB JSON, folded | 16.183 ms | 10.047 ms | 13,754,050 | 9,505,325 |
| 1 MiB JSON, expanded | 16.923 ms | 10.840 ms | 13,803,867 | 9,537,572 |

All twelve measured raw/JSON, size and folding cases reduced median handler time
and allocations. Payload creation and initial history rendering are excluded;
each timed iteration starts from the same bounded 30-record history. Sequential
microbenchmarks do not establish an end-to-end provider latency reduction.
This removes local TUI work without changing tokens, model prompt processing,
tool execution, routing or output retention. Bytes/op measure allocation volume,
not retained heap, process RSS or WebView2 RAM. Full medians and completed logs
remain in the ignored experiment directory.
