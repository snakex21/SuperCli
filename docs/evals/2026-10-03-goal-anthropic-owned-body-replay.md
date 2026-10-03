# R13: owned Anthropic request replay view

Baseline: main f580174 (dev15), Windows amd64, Ryzen 7 5800X3D, GOMAXPROCS=2. Status: accepted production port; all internal/llm tests, go vet and scoped diff-check PASS. No provider/network requests, user data, model/GPU calls or settings changes.

## Reachable work and neighboring mechanism

Ordinary non-Zen Anthropic Complete serializes a request, then calls resp.Request.GetBody and io.ReadAll only to select bytes for anthropicRequestPrefix. Locally encoded request bytes already exist. The candidate retains the actual accepted request selection while removing its second full body copy.

Local codex-main/codex-rs/http-client/src/request.rs lines 9–35 owns an EncodedJsonBody with bytes::Bytes and exposes as_bytes. Lines 181–196 prepare already encoded bytes by sharing the allocation. The port uses the same ownership principle with existing Go readers; it adds no runtime dependency, service, global cache, hash reuse or prompt.

Ordinary Chat Completions/Qwen already builds its text-only image fallback lazily. Request-slice preallocation and immutable-schema borrowing were previously deferred; they are not repeated here. This change has no direct Qwen or Zen effect. Saved-session Anthropic incidence was not established, so no historical gain is claimed.

## Exact behavior

Only the exact private anthropicOwnedRequestBody reader supplies a view. The GetBody factory captures its data argument for each attempt; it never closes over the mutable retry/fallback body variable. Read, WriterTo and Close remain available as with the original bytes.Reader NopCloser. Its private cursor selects exactly the remaining suffix; remaining data is consumed to EOF, while an exhausted/past-EOF cursor stays exhausted without seeking backward.

Foreign, custom and wrapped GetBody readers retain the original io.ReadAll path. Factory/read/close errors, nil-reader recovered provider errors, missing GetBody, wrong Response.Request and partially consumed readers retain their original behavior. Prefixes still derive from the actual accepted GetBody bytes, including negotiated image fallback and redirects. The prefix algorithm, request JSON encoding, signed native history, immutable source history and subscription path are unchanged. The special Zen factory and headers are explicitly untouched.

## Exact fixture proof

Original and candidate source overlays share the final R13 SSE parser and the same helper/test fixtures. Both PASS. Tests cover zero/partial/full/past-EOF replay, nil/empty bytes, short WriterTo, ownership per factory, custom read/close errors, wrapper dispatch, wrong owned/foreign response requests, factory errors and nil-reader panic recovery. Public Complete controls cover signed native prefix, raw signature/unknown native fields, exact builder bytes, unchanged history, negotiated/learned image rejection, 307 body replay, 429 Retry-After:0 replay, cancellation before transport and Zen.

All logged public request SHA/prefix lines match between original and candidate. The ordinary synthetic tool/schema request is 314 bytes, SHA-256 a498dbc732d87282e85713b24883bddd07f420914af7c3502a2ef1fa02b6b08e; native prefix 90a0ebb1505d024f9b8b52dbf18d826ae6791b911a895b6e1f6ac0cb1cc5d347. Foreign/partial factories intentionally select different actual bytes; their respective prefix lines also match.

## Public Complete ABBA measurement

Counterbalanced original/candidate/candidate/original, 200ms each case, one fake RoundTripper with synthetic signed SSE, fully drained Complete channel. The transport consumes the outgoing body without retaining it. No network/model latency is included. Sizes below are synthetic user text, not total encoded body size.

| Text payload | Original time | Candidate time | Original B/op | Candidate B/op | Original allocs/op | Candidate allocs/op |
| --- | --- | --- | --- | --- | --- | --- |
| 16 B | 76.535–77.772 µs | 74.828–77.997 µs | 100,085–100,086 | 99,765–99,767 | 450 | 451 |
| 64 KiB | 2.471–2.503 ms | 2.400–2.434 ms | 1,509,327–1,516,988 | 1,365,234–1,380,002 | 483–484 | 469–470 |
| 1 MiB | 37.799–39.153 ms | 36.090–37.279 ms | 26,380,600–27,085,342 | 23,087,552–23,792,086 | 533–536 | 506–507 |

The principal measured benefit is roughly 3.3 MB less transient allocation per 1 MiB Complete fixture. Short requests add one small allocation and have no dependable latency improvement. Large fixture times show a modest improvement in this bounded measurement; the result does not establish provider prefill/TPS, model tokens, saved turns, idle/resident process RAM or user-visible response latency. Existing prefix canonicalization remains the dominant work in the large fixture. No ABBA rerun was performed after the exact semantic rename.

## Artifacts and production scope

Private evidence: .tmp/goal-harness-round13-2026-10-03/{overlay.json,overlay-baseline.json,owned_body.go,owned_body_test.go,anthropic.go,tests-baseline.txt,tests-candidate.txt,bench-1-baseline.txt,bench-2-candidate.txt,bench-3-candidate.txt,bench-4-baseline.txt}.

Production source scope: internal/llm/anthropic.go, internal/llm/anthropic_request_body.go, internal/llm/anthropic_request_body_test.go. Durable report: docs/evals/2026-10-03-goal-anthropic-owned-body-replay.md. No other production source was edited by this agent.

Final checks: gofmt of the three owned Go files; go test ./internal/llm -count=1 PASS; go vet ./internal/llm PASS; git diff --check for owned scope PASS.
