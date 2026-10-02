# Persistence, images, directory results and GUI stream efficiency — 2026-10-02

This round reduces avoidable local memory and processing, and lets callers request a smaller directory overview. It adds no system-prompt instructions, classifier/model calls, background polling, new cache or image transforms. OpenCode Zen routes, headers and payload handling are unchanged. App data remains portable.

## Changes and measurements

| Path | Change | Observed result | Measurement boundary |
| --- | --- | --- | --- |
| Persistence recovery | Clear only successfully persisted or already evicted queue slots; release the empty backing array | Recovered heap 35,014,872 → 927,784 B, about 32.5 MiB freed | Isolated Go process with 64 queued messages of 512 KiB after a storage outage; no savings claimed for a normal session without an outage |
| Tool images | Try durable image storage before inline base64 encoding | About 27.97 MB less temporary allocation for a 10 MiB image; isolated preparation 7.74 ms → 0.00154 ms | Actual Loop.invoke with a fake successful durable writer; excludes disk and provider transport. Transport still reads and encodes the required image |
| Directory overview | Optional positive list_dir limit, capped by the existing configured maximum | With 700 files, limit=20 reduces model-result bytes 4,276 → 1,536 and avoids retaining the larger output | An explicitly smaller requested listing, not equivalent full-directory content. Omitted limit preserves the existing default; no measured task-turn/token-count claim |
| GUI SSE framing | Scan newly decoded bytes and a tiny separator carry; join fragments when a complete frame arrives | 512 KiB frame split into 4 KiB chunks: 14.62 → 1.31 ms; 64 KiB: 0.348 → 0.200 ms | Node/V8 parser fixture, not a WebView RSS, paint-time or whole-request measurement |

The small-event SSE fixture (1,001 events, 13 network chunks) measured 1.134 → 1.183 ms, about 0.048 ms more for the whole batch. The improvement targets fragmented large frames; it does not establish that every stream is faster. Complete first events are still emitted before reading the next network chunk.

## Correctness

- Persistence FIFO, pending failures, notices and counters are preserved. Only confirmed successful writes and the existing intentional overflow eviction clear slots. Partial failures retain unwritten images, native state and tool-message pairs.
- Image save failures, missing writers, typed-nil durable writers and incomplete references retain the original inline fallback. A real portable session writer preserves pixels, MIME, ID and reference fields. Injected OpenAI and Anthropic transports produced byte-identical complete request bodies for PNG, JPEG, GIF and WebP with inline versus stored images.
- Directory ordering, filters, sandbox checks, cancellation, default cap and breadth-first tree budget remain unchanged. Incomplete listings keep the existing truncation notices. The shared configured tool is never modified by a per-call limit. Names still need enumeration and sorting.
- SSE UTF-8 decoding, LF/CRLF and mixed separators, multiline data, event order, malformed-frame handling, trailing EOF frames, terminal/error checks and callback/upstream errors are preserved. Baseline/candidate comparison covers every two-chunk boundary, one-byte fragmentation and 200 randomized chunk fixtures.

## Harness inspiration

The adjacent Pi ls tool supports an optional limit, making a smaller requested overview possible without a separate tool or long instructions. SuperCli now exposes the same small choice while preserving its own configured cap and output-retention behavior. DeepSeek bounded reads and Claude offset/limit behavior were also inspected; no additional prompt or read-cache policy was imported.

## Validation

- PASS go test ./...
- PASS all 100 GUI tests (scripts/test-ui.cjs), including four new SSE regression tests.
- PASS scoped persistence, list_dir and image tests; the new persistence tests failed on the baseline and pass with the fix.
- PASS scoped tool-image race tests.
- PASS CRLF-aware git diff --check.
- Sources were hashed before and after full tests; all nine owned implementation/test files were unchanged.
- LM Studio at 127.0.0.1:1234 was unavailable. No new live Qwen or Zen comparison was performed; directory results are measured in bytes, not real prompt tokens.

## Local measurement artifacts

- .tmp/context-retention-audit-round2-2026-10-02/pending-retention-report.json
- .tmp/tool-image-externalize-2026-10-02/REPORT.md
- .tmp/list-dir-limit-audit-2026-10-02/report.txt
- .tmp/efficiency-followup-2026-10-02/sse-measurements-final.json
- .tmp/efficiency-followup-2026-10-02/go-tests.json
- .tmp/efficiency-followup-2026-10-02/ui-tests.json

Build and installation results are recorded after validation below.

## Build and installation

All six stripped builds passed: Windows amd64 TUI/GUI, Linux amd64 TUI/GUI and macOS arm64 TUI/GUI. Cross-compilation is not a runtime GUI verification on Linux or macOS. Both Windows binaries passed --help smoke checks.

The installed supercli.exe and supercli-web.exe were backed up before replacement. Their existing hashes were checked before copying, and the installed hashes match the new build artifacts:

- supercli.exe: 26775552 B; SHA256 dff87df2279d34e58597954c937b36e93d366b937c47096844378a71c95be870
- supercli-web.exe: 22412288 B; SHA256 b5bc8575ef4bc076294d911c2f3b7f73bbcc70a1d5d2b81942e4840aba2765da

All nine owned source/test hashes still matched the fully tested files after compilation. No commit, push or release was made.

Build, smoke, backup and installation manifests are saved in .tmp/efficiency-followup-2026-10-02/.
