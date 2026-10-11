# Request preparation and prefill follow-up — 2026-10-11

Two client changes are retained: skip unused Anthropic prefix protection when no
block has a saved prefix, and borrow immutable encoded tool schemas only inside
the immediate OpenAI Chat / Anthropic request serializers. Neither changes
request text, order, tool contracts, model selection, reasoning settings, token
budgets, compaction content, or backend cache/context/checkpoint configuration.

This is client CPU and allocation work. It is **not a measured reduction in model
prefill or summary generation time**. The improvement is independent of model
names within the affected transports; other transports keep their existing
paths. No Qwen, Tenor, website, or downloaded-file production special case was
added.

## Changes and ownership

`protectAnthropicPrefixes` previously canonicalized/hash-encoded the entire
system, tools and chronological messages even when every `Content.Prefix` was
empty. No signature could then be compared or invalidated. A metadata scan now
returns immediately for that case. Any nonempty prefix, including one on a late
ordinary or opaque block, selects the complete old protection path. The hash of
the actual accepted HTTP body remains required and unchanged for subsequent
signed continuation.

`toolSchemaForRequest` shares the existing immutable normalized schema bytes
with two private builders that immediately serialize their local request. Go's
JSON encoder reads those bytes and creates an independent body. No caller sees
the borrowed slice. Cache eviction removes a reference without mutating or
recycling the backing bytes. Existing bounds, FIFO order, key ownership, mode
isolation, oversized bypass and error behavior are unchanged. Normalizers and
the exported `CompileToolSchema` still return independent mutable copies. Codex
and typed Responses assemblers retain their existing ownership contract.

The schema cache's residency limit does not bound all live request buffers;
borrowed values can survive eviction until the request finishes. Allocation
traffic below is not a measurement of resident RAM or VRAM.

## Controlled comparison

Portable artifacts: `.tmp/prefill-followup-2026-10-11/`. Comparison used three
fresh Go test binaries: the exact pre-change sources, the prefix guard alone,
and both changes. Go overlays restored the saved sources; the current checkout
was never rolled back. All binaries used the same shared fixtures.

Five serial, rotated three-way comparisons used `GOMAXPROCS=1`, `-p 1`,
`-test.cpu=1`, 200 ms warm benchmarks and fresh-process `1x` cold benchmarks.
Compilation and fixture construction were excluded. `Complete` includes the
production builder, request preparation, deterministic in-process transport,
response parsing and stream drain. It sends no network inference.

The coding fixtures contain 128 chronological messages, including tool calls,
tool results and retained code. The 24 tool schemas are actual source literals
from the application; their names/descriptions are synthetic. Tiny and
three-tool controls were also included. The larger histories are fixtures, not
claims about a particular user's full conversation.

All 180 benchmark rows were checked independently. The following are median
times and allocation bytes before → after **both** changes. Pair counts are
after < before in the five matched comparisons.

| Complete fixture | Time before → after | Faster pairs | Allocated B/op before → after | Allocs/op before → after |
| --- | ---: | ---: | ---: | ---: |
| Chat, tiny / no tools | 24.184 → 24.001 µs | 2/5 | 74,388 → 74,388 | 87 → 87 |
| Chat, short / 3 tools | 31.843 → 32.489 µs | 3/5 | 77,106 → 76,129 | 92 → 89 |
| Chat, short / 24 tools | 157.500 → 159.388 µs | 3/5 | 126,507 → 103,172 | 113 → 89 |
| Chat, coding 128 KiB | 334.434 → 345.384 µs | 1/5 | 302,928 → 279,598 | 279 → 255 |
| Chat, coding 1 MiB | 1.192 → 1.170 ms | 3/5 | 1,261,435 → 1,238,099 | 280 → 255 |
| Messages, tiny / no tools | 68.451 → 58.467 µs | 5/5 | 101,301 → 93,307 | 394 → 332 |
| Messages, short / 3 tools | 154.940 → 110.205 µs | 5/5 | 136,745 → 111,985 | 1,023 → 646 |
| Messages, short / 24 tools | 1.493 → 0.929 ms | 5/5 | 805,780 → 461,666 | 9,394 → 4,823 |
| Messages, coding 128 KiB | 8.160 → 5.255 ms | 5/5 | 4,417,458 → 2,464,355 | 23,544 → 12,228 |
| Messages, coding 1 MiB | 44.800 → 28.719 ms | 5/5 | 27,637,180 → 16,434,885 | 23,785 → 12,362 |
| Cold Chat / 24 tools | 946.700 → 960.500 µs | 1/5 | 526,016 → 502,848 | 5,432 → 5,409 |
| Cold Messages / 24 tools | 1.952 → 1.469 ms | 5/5 | 1,198,912 → 813,888 | 14,512 → 9,933 |

Cold means an empty client schema cache, **not a cold model or GGUF load**.
There is no demonstrated first-call Chat latency improvement. Raw ranges are
retained in `benchmark-analysis.json`; the small Chat time differences cannot
be presented as a general CPU improvement.

The guard-only result isolates the useful Messages change: approximately 29–39%
less client time for the tool/coding fixtures, 5/5 comparisons. Tiny guard-only
is 13.61%, 4/5, so the combined tiny 5/5 result must not be attributed to schema
borrowing when there are no schemas.

The initial schema-only comparison had a 2.7% slower Messages 1 MiB median,
0/5 faster, with just 7–8 iterations per process. This prompted a second
comparison rather than unconditional adoption. Five serial AB/BA comparisons
with a one-second benchmark interval produced 40 further rows:

| Schema-only comparison against guard-only | Before → after | Faster pairs | Median time difference |
| --- | ---: | ---: | ---: |
| Chat, short / 24 tools | 154.060 → 150.934 µs | 5/5 | 2.03% faster |
| Chat, coding 1 MiB | 1.245 → 1.199 ms | 5/5 | 3.70% faster |
| Messages, short / 24 tools | 914.776 → 912.584 µs | 3/5 | 0.24% faster |
| Messages, coding 1 MiB | 28.636 → 28.577 ms | 4/5 | 0.21% faster |

The earlier CPU regression did not reproduce. Its cause is not established by
these measurements. Schema borrowing is retained primarily for approximately
22.8 KiB and 24 fewer allocations per 24-tool request: 18.45% fewer allocated
bytes for short Chat. The small CPU gain in the longer Chat series is not a
promise for other workloads; Messages has no meaningful demonstrated additional
CPU gain from borrowing.

Three repetitions of prefix-pass controls add 36 rows. Unsigned protection uses
zero allocations. Signed controls retain identical allocation counts
(215 / 1,926 / 6,972 for the three fixture sizes); guarded median times are
0.63–1.95% higher and all baseline/candidate ranges overlap. The signed path
still needs its old work plus the metadata admission scan.

No model/VM processes were stopped, settings changed, cache evicted or slots
pinned. OS memory was observed only at comparison endpoints: free physical
memory 24,055,296 → 24,080,272 KiB. These figures do not establish VM load or a
quiet machine. Comparisons were serial; they do not claim VM-isolated inference.

## Prefill assessment

The current common prompt path already keeps stable instructions and tool
contracts ahead of the refreshed tail. This audit did not find another proven
unnecessary prefix invalidation. Moving reminders, shrinking context/checkpoints
or changing summary reasoning/output limits was not adopted.

The existing adaptive profile deliberately targets time to first model output.
The optional server `timings.prompt_ms` represents prompt processing rather than
that entire wait; the [llama.cpp API documentation](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md#timings-and-context-usage)
documents this timing and its processed/cache token counts. Replacing TTFT with
this field would change the profile's policy and require careful handling of
missing/inconsistent telemetry and late streaming usage. It is not a neutral
serialization optimization. The previously diagnosed 118-second compaction
helper is not fed into the successful-main-call prefill profile, so that helper
did not poison its learned budget. The profile policy was left unchanged.

## Live local Qwen compatibility check

After explicit user authorization, the current production `NewOpenAI` path was
checked twice against the configured local `qwen3.8-27b-uncensored` endpoint.
Configuration was resolved read-only from the active project plus portable
global settings: `high`, thinking enabled. Both outgoing bodies were identical
(709 bytes, SHA-256
`b1f683d2c68aa9a2d7546bc4b1c7083c52b63c207a284470857fb15193e0effc`),
with `reasoning_effort: high`, `cache_prompt: true`, and no output-token override.
No additional retry or tool execution occurred.

The small read-only Go question returned `WYNIK=14` and a normal finish in both
calls. Provider usage reported input 370, output 81/72 and reasoning 72/63 tokens.
Duration was 3.214 / 2.120 seconds and first output 0.992 / 0.159 seconds. These
are consecutive calls with different cache/backend conditions and generated
token counts, **not A/B optimization or compaction-latency evidence**. No model
load/unload, slot/cache control or model configuration API was called. Receipt:
`.tmp/prefill-followup-2026-10-11/live-qwen-smoke.json`.

## Validation and local installation

- Focused ownership / prefix / receipt tests passed.
- Twenty wire receipts are identical across all three binaries, including
  explicit Chat effort/unified settings, Anthropic thinking and matching,
  mismatched and unsigned opaque native continuations.
- Prefix parity tests additionally cover late metadata, cache-marker placement,
  invalid Raw data, numeric/Unicode preservation and unchanged marshal errors.
- Mutation/concurrent encoding/eviction tests preserve borrowed values and the
  independent public-copy contract. Existing schema residency/bypass tests pass.
- `go test -p 1` passed for `internal/llm`, `internal/agent`, `internal/webgui`,
  `internal/app`, `internal/llm/factory`; `go vet` passed for the same packages.
- Source, baseline and binary hashes and all 180 + 40 + 36 raw benchmark rows
  were independently checked. The thirty-five changed/new Go source hashes
  remained unchanged through validation and build.
- Native CLI/GUI builds and version/help smoke checks passed. Both local EXEs
  were replaced atomically with SHA-verified backups. Version remains 1.0.5;
  this follow-up was not committed, pushed or released.

Installed SHA-256:

- CLI: `460CADDE2A024656D7FD6191E34AA3285E45A2D0AF2413487600CBBDE1D64C93`
- GUI: `40E29B4D470C8E804D9493F1300C3EE3E24C9D453ACB0E2FFAFACA9DF53DC07E`

An initial standalone parity run used the repository directory rather than the
Go package directory and could not locate the source fixtures. The runner was
corrected before the complete comparison; that failed setup produced no timing
rows used here. No application change was made to accommodate the runner.
