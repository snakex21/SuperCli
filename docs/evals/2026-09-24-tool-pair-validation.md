# Cheaper validation of tool-call/result pairs — 2026-09-24

## Problem and change

Before encoding a request, the shared toolCallHistoryNeedsRepair guard checks
global call-ID uniqueness and contiguous, complete call/result batches. It
previously built a second map for every assistant tool batch and scanned that
map again after matching results.

One request-scoped table now handles both jobs:
- An absent key is an ID that has not been used.
- A present true value is a call awaiting its result.
- A present false value is a used ID whose result was consumed.

Call registration rejects any present key, including consumed IDs. A result
must consume a true entry exactly once. Since call IDs are unique and every
result consumes one pending call, an equal result count proves batch completion.
An incomplete batch returns immediately, before later messages can satisfy it.

The repair implementation and accepted/rejected histories are unchanged.
No messages are removed by this optimization, no cross-request cache is kept,
and no prompt, setting or model call is added. Chat Completions, Responses and
Anthropic builders share this guard, including local/cloud callers.
The special OpenCode Zen transport, gate tools and headers are untouched.

## Evidence from saved sessions

A read-only aggregate over valid assistant tool_calls_json arrays found 4,280
batches: 3,441 contained one call, 446 two, 190 three, and 203 four or more.
The largest observed batch contained 13 calls. This query does not expose
message contents and does not establish that every stored call is resent.

Therefore the single-call cases below are especially relevant. Eight-call
batches exist in the log but are less common; 32-call batches are synthetic
stress cases, not a claim about typical user sessions.

## Measurements

Windows / Ryzen 7 5800X3D. Initial pair: five 250 ms samples per case, -cpu=1.
Repeat pair: three 250 ms samples, reversed after/before order using a source
overlay. Fixtures are constructed outside timing and valid results are returned
in reverse order to exercise order-independent pairing.

Initial medians (batch count x calls per batch):

| Validator case | Before | After | Allocated bytes before → after | Allocations before → after |
|---|---:|---:|---:|---:|
| No tool calls | 14.70 ns | 14.83 ns | 0 → 0 | 0 → 0 |
| 1 x 1 | 104.50 ns | 51.77 ns | 0 → 0 | 0 → 0 |
| 32 x 1 | 5.939 us | 3.603 us | 3,208 → 3,208 | 7 → 7 |
| 32 x 8 | 48.931 us | 27.607 us | 41,448 → 26,856 | 109 → 13 |
| 32 x 32 | 169.684 us | 115.659 us | 167,384 → 108,760 | 116 → 20 |
| 128 x 8 | 214.926 us | 121.278 us | 167,128 → 108,760 | 404 → 20 |

The repeat pair corroborated the validator improvement: 32 x 1 measured
5.237 → 3.296 us, 32 x 8 measured 43.243 → 27.654 us, and 128 x 8 measured
200.997 → 125.803 us. Histories without tools remain effectively unchanged.

### Full request controls

Building and JSON-encoding a 32 x 8 history, including tool arguments and
560-byte results, showed much smaller and mixed total-time changes:

| Builder | Initial before → after | Repeat before → after | Initial allocated bytes before → after |
|---|---:|---:|---:|
| Chat Completions | 0.421 → 0.402 ms | 0.402 → 0.400 ms | 363,029 → 348,430 |
| Responses | 1.692 → 1.753 ms | 1.538 → 1.544 ms | 747,897 → 733,300 |
| Anthropic | 7.169 → 7.089 ms | 6.853 → 6.931 ms | 3,513,005 → 3,498,411 |

The consistent result is cheaper validation and less temporary allocation.
There is no demonstrated universal speedup to whole-request encoding, model
inference, end-to-end response time or token cost. Allocation bytes are per
operation, not resident RAM. This is a small recurring harness improvement.

## Compatibility checks

- The former validator is retained as a test oracle.
- Deterministic healthy, truncated, role-corrupted, duplicate-ID and missing-ID
  histories plus 1,000 seeded histories with combined mutations produce the
  same decision; canonical messages remain unchanged.
- Explicit checks reject reused consumed IDs and stale results from older
  batches, and accept correctly reordered result batches.
- Existing repair/protocol tests pass.
- Six encoded fixtures (healthy/malformed through each of three builders)
  have matching lengths and SHA-256 hashes before/after. Repair is idempotent
  at the request boundary.
- Full go test ./..., go vet ./..., CLI and GUI builds pass.

No live-model inference is needed to test this deterministic check.

## Reproduce

go test ./internal/llm -run TestToolPairCheck -bench ^BenchmarkToolPair
-benchmem -benchtime=250ms -count=5 -cpu=1

TEMP/TMP and build outputs stay under the application folder. Evidence:
.tmp/tool-pair-validation (source overlay, samples, medians, request hashes,
validation output and installation manifest).
