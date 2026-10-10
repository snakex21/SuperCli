# Canonical schema reuse and completed GUI cache lifetime

Only the standard Responses request path uses assembler-owned canonical schema bytes directly. The assembler had already decoded and re-encoded the schemas, so decoding them again into map trees before the final request was redundant. Legacy callers with arbitrary bytes still take the checked old path. Zen, native replay parsing, tool definitions, request fields and token contents are unchanged.

Actual Complete benchmark uses the exact previous codex_stream.go through a Go overlay for the baseline; prepared-request experiments additionally compare both forms within the same build. Three 150 ms samples, GOMAXPROCS=2. Byte/error parity covers zero/three/thirty tools, short/128 KiB history, Unicode, numeric schemas, invalid native replay and image fallback.

| Actual Complete case | Before time | After time | Before bytes/op | After bytes/op |
| --- | ---: | ---: | ---: | ---: |
| 3 tools, short history | 80.603 us | 48.424 us | 107,632 | 86,733 |
| 30 tools, short history | 458.894 us | 181.646 us | 356,765 | 145,320 |
| 30 tools, 128 KiB history | 1.235 ms | 0.867 ms | 1,549,910 | 948,752 |

The zero-tool path and the three-tool long-history path do not establish a time improvement; do not claim a universal speedup. Allocations per request are not process RSS, and this optimization does not reduce the model token count because the request is equivalent.

Completed GUI assistant paragraphs release incremental descriptors that refer to complete source strings after done/error. Source and DOM stay canonical. A real continued or replaced source rebuilds only the current descriptor from its existing tail; folded reasoning and history remain lazy and recoverable.

The corrected DOM fixture avoids an unrelated global accessor closure holding the whole context alive. Owner-exclusive release proves the descriptor can retain roughly 0.89 MB for a large artificial answer. The direct before/after heap series had a large outlier, so it is not evidence for a WebView2 RSS reduction. DOM/source hashes and all 235 UI tests remain equal/passing.

TUI native !shell now owns a cancelable invocation identity. Ctrl+C/Escape remain in cancelling until the actual process finishes; an old result cannot clear a newer invocation. Tests execute actual native child processes and preserve exact UTF-8 stdout/stderr, exit status, draft and attachment state.

Receipts: .tmp/optimization-oct8-2026/closure-audit/responses-canonical-measures-o.json, responses-canonical-before-o.json, tui-shell-before-o.json, gui-cache-before-o.json and gui-cache-test-fixtures-review-o.json.
