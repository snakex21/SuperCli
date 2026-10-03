# Standard Responses: assemble the final request once

Baseline: `6ab0f1c` / dev14. Evidence and isolated overlays are in
`.tmp/goal-harness-round12-2026-10-03`. No live provider request, model reload,
user program, or provider setting was used for this experiment.

## Repeated work and scope

The standard Responses branch previously serialized a complete typed request,
decoded that entire body into generic maps, added reasoning/sampling/cache
fields, and serialized it again. This repeatedly copied and parsed ordinary
history text even though the CLI had just assembled that text itself.

Local neighboring Codex uses a final `ResponsesApiRequest` with one
`EncodedJsonBody::encode(&request)` before streaming it in
`codex-main/codex-rs/codex-api/src/endpoint/responses.rs`. The adopted mechanism
is final request assembly before serialization, with SuperCli's existing wire
contract retained.

The new path applies only to standard Responses endpoints outside OpenCode Zen.
Chat Completions, the special Zen dialect, subscription Codex transport, and
the public byte-input `prepareStandardResponsesRequest` are unchanged.

## Implementation and compatibility

The existing typed assembler is shared with its original byte builder. The
standard path builds both the primary request and any image fallback before
preparing either body, preserving the original stage order and error priority.
A private helper constructs the same final generic maps directly from freshly
assembled typed fields and then serializes the final body once.

Schemas and opaque native items still use the original generic JSON numeric
semantics and final key ordering. Native history is never modified. Invalid
UTF-8 in typed fields and opaque parser errors use the original full
marshal/decode/marshal path; this preserves replacement-character byte spelling
and exact errors. Non-finite sampling remains rejected. Future-field tests
cover field names, types, full JSON tags, and `omitempty` behavior.

A smaller item-by-item encoding alternative passed equivalence tests but did
not offer a worthwhile speed or allocation improvement and was rejected.

## Measurements

Windows amd64, local Go toolchain, Ryzen 5800X3D. A fake in-memory transport
consumes each real `NewResponses(...).Complete(...)` request and returns a
terminal SSE event. It performs no network call or model inference. The
counterbalanced order was baseline, direct, itemwise, itemwise, direct,
baseline, with 150 ms per fixture. Schema cache state was warm equally.

Each synthetic request has two copies of the listed UTF-8 text, a small system
instruction and follow-up, and one tool. Final bodies match the old bytes
exactly, including original-body SHA-256 goldens.

| Text bytes each | Final body bytes | Baseline Complete time | Direct Complete time | Baseline transient B/op | Direct transient B/op |
|---:|---:|---:|---:|---:|---:|
| 45 | 737 | 53.7–59.2 µs | 40.2–40.4 µs | 85,440 | 83,148–83,154 |
| 140,445 | 381,377 | 5.68–5.85 ms | 0.809–0.841 ms | 3,803,828–4,070,285 | 756,311–890,035 |
| 1,123,515 | 3,046,589 | 45.94–53.40 ms | 6.42–6.91 ms | 33,428,462–39,717,218 | 8,471,191–9,112,090 |

The tiny control uses 227 rather than 293 allocations. Itemwise uses 319; its
largest case costs 47.32–53.78 ms and 45,832,586–51,554,402 B/op. Allocation
counts are transient request costs, not retained RAM. Large byte measurements
vary with encoder pool and GC state; ranges are reported without precise
percentage claims. These measurements describe the equivalent direct-map
overlay; no further production benchmark was run after the shared-assembler
port and stage-order correction.

Wire bytes are unchanged, so this is not a reduction in model input tokens or
turns. No claim is made about historical incidence, Qwen/Chat Completions,
provider prefill throughput, or decoder TPS.

## Verification

- 240 exact combinations of text, tools, effort, reasoning capability, and
  finite/non-finite sampling.
- Nil input, instruction-only input, malformed schemas, invalid UTF-8, escaping,
  duplicate native keys, unknown native fields, large numeric values, overflow,
  incomplete/non-object native JSON, and image/text fallback.
- Unchanged original messages and native payload bytes; exact fake-transport
  standard and Zen bodies; original public-body goldens at all measured sizes.
- Actual public error-priority controls for schema versus image build errors,
  image build versus native preparation errors, and native versus sampling
  errors. Rejections do not call the transport.
- `go test ./internal/llm ./internal/llm/providers -count=1` and
  `go vet ./internal/llm ./internal/llm/providers` pass with portable caches and
  temporary files under the repository. `git diff --check` passes.
