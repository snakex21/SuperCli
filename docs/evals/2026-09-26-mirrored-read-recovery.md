# Native/text mirrored read recovery — 2026-09-26

## Reproduced fault

During the [synthetic worker-report live replay](2026-09-26-worker-report-live.md), Qwen emitted a single logical read in both accepted formats in one response:

- Native read_output: offset 3072 and limit 8192 as JSON numbers.
- Text sentinel read_output: the same output handle, offset and limit, parsed as strings.

The stream consumer appended both calls. Both executed, and the same roughly 8 KB evidence block entered the next model request twice. A subsequent read at offset 11264 was legitimate. This yielded three reads for two ranges, even though the model had not requested a later retry.

## Change

The common stream consumer now records which calls came from text parsing and removes a text mirror only when there is an equivalent valid, registered, read-only native call in the same read-only sequence of a single response.

Registry.ReadOnlyCallKey uses the same argument coercion and schema validation as execution, then canonical object-key ordering. JSON numbers retain their exact representation instead of being collapsed through float64; different large integer offsets stay distinct.

Matching is one-to-one. All native calls and their original IDs remain intact. Native-only repetitions, text-only repetitions, extra unpaired calls and different arguments remain intact. Writes, unknown tools and invalid calls break the matching sequence, so reads on opposite sides of an edit are not combined. No cache or suppression carries over to another response or turn.

Pure native/text responses return without hashing. The additional comparison work is limited to responses that mix the two protocols. No prompt, model call, provider transport, Zen-specific route or UI history contract was added.

Files:
- internal/agent/stream_consume.go
- internal/agent/stream_mirror.go
- internal/tools/core/readonly_identity.go

## Evidence

The regression test ran the actual coordinator Loop with a deterministic provider and failed before the change: the read handler executed twice for either native-first or text-first order. It now executes once, and the next model request contains one native tool call and one matching result.

A live Qwen run after the change again emitted a native call plus its sentinel mirror. Only the native call executed. Both ranges remained available and the final answer retained all nine findings, priorities and NOT_RUN.

| Observed thin legacy replay | Before | After |
| --- | ---: | ---: |
| Coordinator model calls | 3 | 3 |
| Executed reads | 3 | 2 |
| Input tokens across calls | 12,276 | 8,942 |
| Output tokens | 1,078 | 851 |
| Recorded seconds | 74.54 | 35.21 |

The tool/result reduction is directly supported by the wire trace and deterministic request-history test. The separate live trials differ in generated output, cache state and other load (the final trial ran alongside Go checks). The observed time difference must not be attributed solely to this fix or advertised as a general speedup. Input tokens fell about 27% in this pair; the stable guarantee is that the mirrored result is not inserted twice.

## Validation

- Before/after execution regression, including preservation of native IDs.
- Independent duplicates and mutation/unknown/invalid barriers.
- Every split point of sentinel and XML blocks across streamed chunks.
- Equivalent scalar and encoded-array coercion, invalid inputs and distinct large integers.
- Full go test -timeout=90s ./... and go vet ./... passed.
- Live replay retained correct final evidence after removing the extra execution.

Code tests: internal/agent/stream_mirror_test.go and internal/tools/core/readonly_identity_test.go.

Local artifacts: .tmp/mirrored-read-2026-09-26/ for tests/builds; .tmp/worker-report-live-2026-09-26/local-wire/ and local-wire-fixed/ for synthetic provider traces and results.

## Installed build

Both Windows binaries were rebuilt and installed after the passing checks. CLI --help passed. Previous binaries and installation hashes are saved under .tmp/mirrored-read-2026-09-26/before/ and installed.json. Running instances need a restart to use the new build.
