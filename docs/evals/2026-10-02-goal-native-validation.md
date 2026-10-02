# Native chat validation in outgoing request preparation — 2026-10-02

## Result

Native chat validation was decoding text that the request builder immediately decoded again. The accepted change lets ReasoningBlock.Validate validate a canonical, single-field chat payload without materializing the unused text or object map. The unchanged request builder still decodes the exact native text. No reasoning, history, input tokens, tool calls, or instructions are removed.

On the measured Loop preparation sequence for three substantial saved contexts, CPU decreased by 16–26% (approximately 1.1–2.5 ms per preparation) and temporary allocated bytes decreased by 28–37%. This is host preparation work, not a measurement of network latency, model prefill/generation, GUI frame time or application RSS.

## Evidence from saved sessions

The six anonymous sessions are the bounded sample already used by the structured tool-output frequency audit. A bounded readonly SQLite audit reconstructed their existing persisted model projections plus messages after through_seq. The complete archive is not substituted for the saved projection.

The archives contain 1,575 native chat blocks; their largest native payload is approximately 47 KiB. Counts and sizes are redacted aggregates. No private prompts, native text, session IDs, paths, credentials or output handles appear in this report. Replay data exists only in ignored .tmp files.

The measured replay performs the actual coordinator Loop methods: two EstimateNextRequestTokens calls, definition snapshot selection, prepareProviderMessages(true), previous-reasoning policy with discard enabled, native scope projection, dormant media projection, Message.Validate and the unchanged OpenAI request builder. A small fixed set of eight ordinary tool descriptors supplies controlled schema overhead. The existing Zen gate descriptors are added through the unchanged gate helper when the selected originating endpoint is Zen; no provider source, endpoint, header, or special Zen path is changed.

| Sample | Prepared messages | Retained native blocks | Native payload bytes | Encoded body bytes | Estimated request tokens |
| --- | ---: | ---: | ---: | ---: | ---: |
| S03 | 439 | 161 | 220,197 | 546,494 | 136,152 |
| S04 | 20 | 0 | 0 | 8,981 | 2,429 |
| S05 | 25 | 3 | 744 | 21,999 | 6,166 |
| S12 | 276 | 112 | 353,921 | 944,383 | 243,801 |
| S13 | 31 | 12 | 16,356 | 60,073 | 15,454 |
| S14 | 170 | 50 | 216,071 | 694,103 | 177,252 |

The native origin matched a known local/Zen endpoint in five sessions. S04's originating scope did not match the known candidates, so the production scope filter removed native state; it is a useful no-native control, not evidence of a gain.

The clock stamp value alone is fixed after preparation so independent baseline/candidate processes can compare exact body hashes. Both versions run the production stamp preparation. All six body SHA256s, lengths, message/native counts and estimated token counts are exactly equal before/after.

These are direct preparation replays, not full Run executions. A normal Run can compact a large context before this stage, and its selected tool set/window/model settings may differ. Consequently these numbers do not predict a particular user's end-to-end waiting time.

## Matched full-sequence measurement

Go 1.26.2, Windows/amd64, GOMAXPROCS=2. Each exact-source baseline/current benchmark has four samples of 50 operations; table entries are medians. Compilation, SQL, fixture decoding and initial definition/schema warming are outside timing. No GPU, network, provider request or catalog request is involved.

| Sample | Old CPU | New CPU | Old allocated bytes/op | New allocated bytes/op | Old allocations/op | New allocations/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| S03 | 7.138 ms | 5.997 ms | 3,383,285 | 2,438,461 | 5,098.5 | 3,008.5 |
| S04 | 49.646 µs | 58.765 µs | 45,216 | 45,216 | 121 | 121 |
| S05 | 108.409 µs | 131.363 µs | 70,728 | 65,493 | 192 | 153 |
| S12 | 12.135 ms | 9.588 ms | 4,886,304.5 | 3,066,201 | 3,577 | 2,098.5 |
| S13 | 592.934 µs | 459.112 µs | 240,644.5 | 177,411 | 442 | 285.5 |
| S14 | 6.678 ms | 4.968 ms | 2,852,261.5 | 1,926,764 | 1,692 | 1,038.5 |

Small S04/S05 CPU results are noisy and did not improve in this full-sequence run. Earlier validation+encoding-only measurements showed improvements on their larger unprojected native sets, but that does not justify claiming a reliable small-request speedup. S04 takes the same non-native path and has exactly unchanged allocation counts/bytes. S05 retains only three tiny blocks after the existing policy. The substantial native contexts show the practical gain.

Allocation bytes are temporary allocations, including encoding/json's existing pooled work buffers; their totals can vary with GC/pool behavior. They are not retained cache size or a process working-set measurement.

## Exact behavior and ownership

Only a known-origin ReasoningChat block beginning with one of the three literal field prefixes can take the shortcut. Its complete value must be a valid JSON string or literal null, preserving encoding/json's historical null-to-empty-string behavior. JSON validation on the whole value prevents extra/duplicate keys from taking this path.

All other payloads use the existing validateParsed parser: malformed JSON, unsupported fields, different formats, missing origin, escaped keys, duplicate keys, noncanonical object syntax and nontext values retain their exact existing errors and semantics. No Unicode TrimSpace is performed before grammar validation: non-JSON Unicode whitespace must remain invalid.

The legacy typed-map request fallback remains unchanged, including its treatment of an earlier wrong-typed duplicate followed by a final string. Invalid UTF-8 inside strings remains compatible with encoding/json's replacement behavior.

There is no new cache, map/table, Loop state, ownership transfer or invalidation mechanism. The shortcut does not mutate Data and allocates zero heap objects in the steady-state canonical validation checks, including Unicode payloads. It benefits cold and warm calls alike. Existing provider validation still happens before transport scope filtering; it has not been moved or weakened.

## Validation and artifacts

Production:
- internal/llm/reasoning_history.go: canonical positive Validate path only.
- internal/llm/reasoning_validation_test.go: differential parser/error parity over all byte values, malformed/duplicate/escaped keys, three fields, null/bool/numbers, formats/origin, Unicode whitespace, invalid UTF-8 and seeded invalid data; exact wire text/state and zero-allocation checks; concise repeatable benchmark.

Completed checks:
- go test ./internal/llm -count=1: PASS.
- go test ./internal/agent -run Reasoning -count=1: PASS.
- go vet ./internal/llm: PASS.
- Exact-source replay body/token/hash parity: PASS, six contexts.
- BenchmarkNativeChatValidation: completed, synthetic 256/1,024/8,192/32,768-byte targets, no network.

All artifacts are under .tmp/goal-prompt-prep-2026-10-02-round5:
- pipeline-baseline-overlay.json / pipeline-candidate-overlay.json and pipeline-metrics.json: final actual preparation sequence.
- production-baseline.txt / production-candidate.txt / production-metrics.json: earlier validation+encoding isolation.
- native-validation-benchmark.txt, scoped-llm-tests.txt, scoped-agent-tests.txt, scoped-vet.txt.
- production-source.diff and frozen-sources.json.
- Private replay/selection/identity fixtures are intentionally untracked and are not publishable documentation.

Portable Go cache/temp paths remain inside .tmp; existing module cache was read-only, with network module fetching disabled. No user config/model/state was changed.

## Deferred candidate

Request-slice preallocation and internal readonly borrowing of normalized schema bytes were also measured in a separate prototype. Allocations could decrease, but CPU results were weak/noisy for this sample; schema borrowing would add another private ownership contract. Neither candidate was ported, and no provider builder/schema implementation was changed.
