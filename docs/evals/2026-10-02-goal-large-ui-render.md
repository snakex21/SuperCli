# Large tool-result rendering audit — 2026-10-02

Base: main `3eae0a5`. Windows amd64, Ryzen 7 5800X3D; Go benchmark with GOMAXPROCS=2, three 200 ms samples per case. All experiment files and Go cache/temp files remain in the repository's .tmp directory. No user application, model, desktop capture, or WebView2 process was launched.

## TUI change

ToolResultFull previously allocated one substring entry for every output line before keeping only four folded lines or forty expanded lines. It now counts lines for the same metadata and splits only enough entries to obtain the visible prefix. toolDisplayOutput skips JSON decoding for text whose first non-whitespace character is not an opening brace; unsupported JSON and malformed objects still pass through unchanged. Process objects retain the existing stdout/stderr decoding and the stored/model transcript is untouched.

Controlled before/after overlay, identical fixture and method call (roughly 1 MiB multiline output, folded, ctx_execute):

| Payload | Median before | Median after | Allocated before | Allocated after | Allocation count before → after |
| --- | ---: | ---: | ---: | ---: | ---: |
| Plain log/text | 272.8 µs | 34.0 µs | ~1,299,400 B/op | 4,760 B/op | 81 → 73 |
| JSON stdout/stderr | 5.75 ms | 5.52 ms | ~4,486,330 B/op | ~4,248,835 B/op | 87–88 → 87–88 |

The original implementation was retained only in an ignored overlay fixture to compare exact rendered bytes. Parity passed for empty output, whitespace, unsupported JSON, malformed objects, stdout/stderr objects, Unicode, 0–45 lines, 40,000 lines, and a very long single line in both folded and expanded modes.

Final tracked small-row benchmark: 1,320 → 1,072 B/op and 49 → 42 allocations. Sample median 8.557 → 8.357 µs is within ordinary timing variation; the allocation reduction is deterministic. The tracked large-payload benchmark uses read_file and one fewer input row than the overlay fixture: final plain text measured median 34.4 µs, 5,272 B/op, 74 allocations; structured output measured median 5.11 ms, ~4,232,490 B/op, 87–88 allocations. These final absolute values are not substituted into the different fixture's before/after comparison.

Validation: complete go test ./internal/ui/tui -count=1 passed; focused visible-line boundary and JSON regression tests passed.

Limits: B/op measures cumulative allocations per render call, not retained RAM or process RSS. Large structured JSON still needs its existing decode. Counting scans the complete text; the stored original result remains available. Very long individual visible lines retain existing compaction work. This change does not claim a reduction in model tokens or turns.

## GUI completion change

The chat completion path flushes on done, after SSE reaches EOF, and again when sealing the segment. The renderer now records a successfully rendered authoritative source and skips repeated rendering of that exact source while preserving smartScroll calls. Partial paced paints and rendering failures invalidate that record; replacement with different source bytes still triggers fresh rendering.

A serialization-only deterministic DOM fixture with 1,056,057 bytes of reasoning, answer text, and code reduced full parser/render calls from 3 to 1 while retaining 3 scroll updates. The displayed HTML remained byte-identical and existing DOM nodes retained identity. Fixtures also passed recovery replacement, failed-render retry, full paced-text drain, reasoning closure, and checkpoint cleanup.

Limits: the dependency-free DOM double does not measure browser layout, compositor work, FPS, WebView2 heap, or native RAM. Thirty-eight focused parser/checkpoint, incremental Markdown, first-paint/pacing, and scroll-frame tests passed for the final tracked implementation.

Artifacts: .tmp/ui-large-response-audit-2026-10-02 contains baseline/candidate source overlays, exact-output replay, benchmark outputs, and gui-terminal-flush-fixture.cjs. No package installation was required.
