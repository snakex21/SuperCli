# Goal evaluation: reuse validated native chat replay text

Date: 2026-10-02. Audit baseline: main `cb7f507`.

## Change

The Chat Completions request path decoded each native chat payload three times at the object level: `Message.Validate`, another `ReasoningBlock.Validate` inside `nativePayloads`, and the final `map[string]string` decode inside `applyChatReasoning`. Native validation now exposes the decoded chat field/text internally. The request builder reuses that text for the provably single-field canonical JSON form produced by the native chat accumulator.

The guard checks an exact known field prefix, the validated original raw value length and the final closing brace. It trims outer whitespace for comparison only; it does not edit the payload. Other spellings use the existing typed-map replay decode. This retains an important legacy edge: a duplicate field whose earlier value is a number and final value a string passes RawMessage validation, but typed-map replay decoding retains the earlier type error and suppresses replay. An unconditional optimization failed that differential case and was rejected before tracked implementation. Escaped keys, internal whitespace and duplicate-key spellings use the legacy path.

Public validation keeps the existing origin, format, JSON syntax, object, exact-key, field-count and value-type checks and error strings. Native Data, scope, model, provider signatures and history are untouched. No parsed payload is cached. No new permanent prompt, model call, background service, dependency or special Zen-path change was added.

This reduces preparation CPU and transient allocations. Token input, output quality, number of agent turns and required canonical history retention do not change.

## Deterministic measurement

Windows amd64, Go 1.26.2, Ryzen 7 5800X3D. The fixture interleaves eight user instructions and native assistant blocks, followed by a continuation request. The benchmark runs the production Complete preparation stages: message validation, native endpoint/model filtering and the actual OpenAI JSON request builder. It excludes HTTP and upstream generation. Three 200ms runs per fixture, medians:

| Native text total | Wire bytes, unchanged | Baseline ns/op | Candidate ns/op | Baseline B/op | Candidate B/op | Baseline allocs/op | Candidate allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 4 KiB | 5,208 | 149,838 | 118,783 | 48,798 | 40,029 | 307 | 227 |
| 64 KiB | 66,648 | 1,945,711 | 1,571,009 | 464,097 | 393,170 | 309 | 229 |
| 512 KiB | 525,400 | 15,495,850 | 12,784,706 | 4,515,061 | 3,796,158 | 326 | 244 |

The measured time reductions are 20.7%, 19.3% and 17.5%; allocated-byte reductions are 18.0%, 15.3% and 15.9%. Small/medium fixtures eliminate ten allocations per native block (80 per request). The large fixture has more JSON-buffer/GC variation and short iteration counts. These are synthetic preparation measurements, not a measured live-provider latency or process RSS reduction. There is no gain for requests without native chat replay. Noncanonical input retains the legacy decode work.

The retained benchmark is `BenchmarkNativeReasoningRequestPipeline`. The ignored `.tmp/native-reasoning-parse-audit-2026-10-02` directory contains the original baseline/candidate overlays, completed measurements and aggregate metrics. No private conversations, credentials, IDs or project contents were used.

## Validation

Passed with tracked source:

- `TestNativeReasoningValidationContracts`: 49 explicit valid/invalid cases with fixed expected validation errors for origin/format/JSON, null/string/scalar forms, exact case/escaped keys, duplicate keys and native Responses/Anthropic contracts. Original payload and signature-prefix bytes remain unchanged.
- `TestNativeChatReplayGeneratedParity`: 960 canonical, whitespace and duplicate-key variants, including earlier invalid scalar values, escaped Unicode and unpaired surrogates, compared to a small legacy replay reference. No copy of the previous complete validator is embedded in the permanent tests.
- `TestNativeChatReplayTransportAndHistory`: real OpenAI Complete against a local httptest server, native wire fields equal to legacy replay, original history unchanged, malformed input rejected before HTTP; includes the duplicate-key retained-error edge.
- Existing native Chat/Responses/Anthropic round trips, signature/prefix protection and related reasoning/OpenAI/Anthropic regressions.

Command:

```text
go test ./internal/llm -run "(Native|Reasoning|Anthropic|OpenAI)" -count=1
```

Result: PASS, 2.513s. No stage, commit or executable build was performed by this worker.
