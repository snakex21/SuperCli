# Reuse prepared tool schemas when creating workers

Date: 2026-09-26. Shared agent registry construction; no provider, prompt or Zen changes.

## Evidence

restrictedRegistry copied each allowed Tool through Register. Register reparsed JSON Schema, normalized it, compiled constraints/regular expressions and built enum hints, although the source registry already held the exact immutable compiled validator. The code path runs for every worker, including deferred tools whose schemas are not yet sent to the model.

A benchmark uses the production tool schemas from the existing workerSchemaFixture (file, command/process and document tools) and the real restrictedRegistry plus EnsureReadOutput path. Base registry construction and model/network time are excluded. This is a local setup measurement, not a claim about the whole delegation time or the full application catalog.

## Change

Registry.RegisterFrom copies an already registered Tool together with its immutable compiled validator. It does not inherit visibility, activation or discovery state. Existing Register still validates and compiles newly supplied definitions, and duplicate/missing-tool errors leave the target unchanged.

Worker setup uses this path for allowed functional tools. Registry-bound tool_search/invoke_tool callbacks are still rebuilt for the child; read_output is still bound to the shared family output store. Allowlist/delegation restrictions remain unchanged. No global schema cache, persistence changes, larger instructions or model calls are introduced.

## Measurement

Windows / Ryzen 5800X3D; median of three 300 ms samples.

| Worker fixture | Before | After | Allocated bytes before -> after | Allocations before -> after |
| --- | ---: | ---: | ---: | ---: |
| general | 0.477133 ms | 0.024160 ms | 212,537 -> 16,735 | 2,514 -> 189 |
| code | 0.438337 ms | 0.034353 ms | 212,686 -> 16,895 | 2,514 -> 189 |

About 92–95% less local registry setup time and 92% fewer allocated bytes in this fixture. Absolute saving is about 0.40–0.45 ms per setup. This removes CPU/allocation work; it does not save a model turn or change prompt size.

## Validation

Tests exercise numeric coercion, range/enum/pattern validation, sealed root arguments, no implementation call after invalid arguments, empty/boolean schemas, duplicate/self-copy/nil/missing-source cases, independent activation and concurrent child validation. Existing worker tests cover scoped discovery, read-only restrictions, deferred tool availability and output references.

Race detector remains unavailable in this environment (CGO disabled; no gcc on PATH). Concurrent fixtures run as ordinary Go tests, not race-instrumented tests.

Artifacts: .tmp/worker-registry-setup-2026-09-26/{before,after,validation-tests}.json.

Full go test ./... and go vet ./... passed. CLI/GUI builds and CLI --help smoke passed. Both installed executables match their build hashes; previous binaries are backed up under .tmp/worker-registry-setup-2026-09-26/before. Restart running instances to use the change.
