# Single-line SSE data without a second payload copy — 2026-10-03

Baseline: f580174 / dev15. Scope: parseSSE, reached by the native Anthropic Messages provider. OpenAI/Qwen and Codex/Zen use separate scanners and are unchanged.

## Change

The parser already receives an owned immutable scanner.Text string. Previously it immediately copied every data value into a strings.Builder, even for the usual single-data-line event. The first value is now kept as that owned string; a builder is allocated only after a second data line. Multi-line concatenation, empty-data behavior, event-name updates, trailing flush, scanner limits, callback errors and retained callback strings keep the original behavior. No borrowed scanner byte slice, unsafe conversion, new cache/timer/dependency, prompt or provider request is introduced.

## Matched evidence

Windows amd64, Ryzen 7 5800X3D, Go 1.26.2 with portable caches and GOMAXPROCS=2. A-B-B-A, 150 ms/case. Public Complete includes the real request builder, in-memory HTTP RoundTripper, SSE parser, JSON decoder and Delta channel; no network, GPU or model execution. Fixture stream contains the stated text chunks followed by message_stop. B/op is transient Go allocation, not retained heap or native RSS.

| Public Complete / chunks | Baseline B/op | Candidate B/op | Baseline time | Candidate time |
|---|---:|---:|---:|---:|
| 128 × 8 B | 206,614–206,624 | 194,297–194,307 | 373–415 µs | 363–401 µs |
| 64 × 4 KiB | 1,325,153–1,325,159 | 1,013,684–1,013,724 | 1.913–1.985 ms | 1.742–2.015 ms |
| 8 × 64 KiB | 2,519,549–2,519,670 | 1,929,503–1,929,650 | 3.498–3.585 ms | 3.034–3.382 ms |

The reproducible benefit is one fewer payload copy per single-line event. Public allocation savings are about 12 KB / 311 KB / 590 KB per fixture. Small/4 KiB timings overlap; no general latency, model TPS/prefill, token or turn-count improvement is claimed. Isolated parser measurements also reduce B/op (64 KiB fixture 1,376,624 → approximately 786,778), but the public Complete measurements are primary.

## Verification

Baseline and candidate overlays passed SSE/Anthropic controls (12.507 /12.776 s). Differential oracle compares complete events/errors for 309 fixed/generated inputs under ordinary reads, one-byte reads, injected reader errors and early callback stop. Controls include Unicode, NUL/invalid UTF-8, CRLF, empty data fields, multi-line data, event changes, missing terminators and the existing 1 MiB scanner cap. A separate test keeps 80 callback event/data strings through later scanner-buffer reuse and verifies every complete value. Existing native thinking/signature/tool/cancellation tests remain unchanged. Final tracked source tests/vet recorded after port.

Ignored portable receipts and all ABBA outputs: .tmp/goal-root-round13-2026-10-03/sse-parser. No private prompt, session or pixels used.

Final tracked controls: original-parser overlay and final parser both passed the complete llm package (16.069 /16.391 s); go vet ./internal/llm passed. Added public multiline Complete control (64 ×4 KiB with JSON members split across data lines) retains approximately 1.332 MB/op in both arms and overlapping 1.773–1.843 ms timings; no multiline benefit is claimed. The single message_stop frame accounts for one fewer small allocation.
