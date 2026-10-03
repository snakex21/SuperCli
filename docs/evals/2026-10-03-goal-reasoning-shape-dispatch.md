# Structured reasoning decoder shape dispatch

Baseline main 78c5366 / dev16. The minimal production port follows the completed isolated prototype and bounded proof. No live requests, model calls, native applications or settings changes.

## Source mapping

Local Codex `codex-rs/codex-api/src/sse/responses.rs:306` dispatches known JSON value types before extracting text. pi `packages/ai/src/providers/openai-completions.ts:318–331` checks `typeof string` for visible reasoning. DeepSeek `llm-deepseek/src/sse.ts` parses a complete SSE once to an object, with explicit type checks in replay helpers. We borrow only the type dispatch idea; their narrower extraction semantics are unsuitable for SuperCli. OpenCode consumes typed reasoning events rather than defining a distinct applicable decoder optimization.

Current `internal/llm/openai_parse.go:extractStringLeaves` tries string, then array, then object `json.Unmarshal`. Each object therefore incurs two unsuccessful decoder calls; each array incurs one. Recursion repeats that work. The existing reachable public structured-reasoning test confirms this source path, but recent saved-session incidence is not established.

## Isolated candidate

The first byte after JSON ASCII whitespace (space/tab/CR/LF) selects the same string, array or object decoder. Each decoder receives the complete original raw JSON; input is neither normalized nor rewritten. Primitive/null/other starts still yield empty text. Malformed recognized strings/arrays/objects still yield empty text after the same selected decoder fails. Variables are branch local. The existing preferred key order (`text`, `content`, `value`, `delta`, `thinking`, `reasoning`), sorted fallback keys, metadata suppression and recursion remain exact. Top-level framing/type validation, signatures, replay/history, schemas, Zen/subscription routing and transport are untouched.

Ignored evidence is retained in `.tmp/goal-harness-round14-2026-10-03`: `candidate.diff`, the exact source/oracle/test prototypes, both overlays, test logs and four benchmark outputs. Original and candidate overlays use the exact same current request builders and SSE framing implementation. The port changes only `internal/llm/openai_parse.go`; durable differential/public controls are `reasoning_shape_dispatch_test.go` and `reasoning_shape_oracle_test.go`, with semantic helper names.

## Completed parity proof

Original and candidate test executions both passed; whole script completed in 5.813 s. Differential fixtures include fixed string/array/object/null/primitive payloads; duplicate and escaped keys; metadata/preferred/fallback behavior; nested containers; invalid UTF-8; malformed, truncated and trailing JSON; all 256 byte values; 512 seeded arbitrary raw payloads; and four whitespace wrappers. The independent recursive oracle is the exact original implementation, including its existing ordering policy. Input raw bytes are checked unchanged.

Public in-memory `OpenAI.Complete` fixtures fully consume outgoing requests and Delta streams on generic and Zen URLs, covering plain text, typed `reasoning_content`, flat generic `reasoning`, structured objects and structured arrays. Thirteen request/Delta/native/error hash controls are byte-identical across both variants. Full Delta hashes include native replay payloads, text, reasoning and usage. Wrong typed reasoning content, malformed top-level JSON and trailing garbage preserve error strings.

Generic request: 274 bytes, SHA-256 `ca4f9108233178d4d41f543464723af9958243d614088001aaa96896d7ee3b0d`. Zen request: 622 bytes, SHA-256 `7253024dee062804f7e2ef70fb88542b8184c397a3681b04aca8f3cfe3c0c46f`. Requests are synthetic; no user data, executed commands, DNS or network connections.

## Bounded public Complete measurement

Completed one ABBA sequence (original/candidate/candidate/original), Windows amd64, Ryzen 7 5800X3D, portable Go caches, `GOMAXPROCS=2`, 150 ms per case, 128 chunks × 32 bytes. The fully consumed public path includes request construction, framing/typed decoding, channels and Delta collection. The script completed in 9.322 s; no repetition was run to seek a favorable timing. Raw outputs are `bench-1-baseline.txt` through `bench-4-baseline.txt` (middle files are candidate).

| Case | Original time range | Candidate time range | Original B/op | Candidate B/op | Original allocs/op | Candidate allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Plain, helper bypassed | 695.821–767.811 µs | 830.463–862.090 µs | 333,892–333,939 | 333,898–333,922 | 3,852 | 3,852 |
| Typed native flat, helper bypassed | 763.484–799.696 µs | 830.278–848.688 µs | 387,628–387,872 | 387,492–387,887 | 4,255 | 4,255–4,256 |
| Generic flat string | 771.013–834.154 µs | 907.581–910.457 µs | 399,239–399,444 | 398,856–399,168 | 4,384 | 4,383–4,384 |
| Structured object | 1.358–1.416 ms | 1.374–1.673 ms | 581,835–581,850 | 517,265–517,273 | 7,693 | 6,669 |
| Structured array | 1.481–2.097 ms | 1.372–1.523 ms | 620,203–620,221 | 521,805–521,821 | 8,590 | 6,925 |

Object fixtures save approximately 64.6 KB and 1,024 allocations per 128-chunk Complete. Array fixtures save approximately 98.4 KB and 1,665 allocations. Plain/typed-flat allocation results are equivalent within small pool/runtime noise, as expected for paths that bypass the helper. Generic-flat allocation changes are below 0.2% and not attributed as a gain.

Timings do **not** prove a latency improvement or absence of timing regression: even the untouched plain path was slower in both middle candidate runs. Object timings overlap; array timing dispersion is large. Flat candidate timing is worse in this bounded sequence. We do not infer a provider or application speed gain from allocation results.

## Decision and limitations

There is an exact, small conditional allocation improvement for structured reasoning. The narrow port is accepted for this transient allocation reduction, with no latency promise. Frozen prototype hashes and byte equality of the production parser against baseline were checked immediately before porting. Only semantic test/helper renames and an oracle comment accompany the already formatted, measured source. No new tests or benchmarks were started during the other agent’s CPU window; final full tests/vet belong to root integration. Unknown real structured-field incidence and inconclusive timing mean this is not evidence that ordinary local Qwen is faster. Typical nonempty typed `reasoning_content` and plain output bypass this helper. Request bytes/tokens, model turns, prefill, decoder TPS, retained heap and application RSS are unchanged or unmeasured. No additional benchmark repetition, broader matrix or candidate is proposed in this round.

Other reviewed leads are already integrated/deferred: ordinary Qwen image fallback is lazy; Anthropic owned request replay and single-line SSE copies are integrated; immutable schema borrowing/request preallocation remain deferred; broad one-pass OpenAI raw+typed decoding would risk exact error/duplicate/case behavior and is not proposed.
