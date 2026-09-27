# Actionable enum errors and cheaper argument validation

Date: 2026-09-26. Shared tool registry and validator, used by local/cloud agents, workers and both interfaces.

## Trace-backed recovery issue

In the fixed GunMayhem snapshot, sequence 1360 calls remember with scope=project and type=project. The tool rejects the type but replies only `$.type: value is not in enum`. At sequence 1362 the agent calls tool_search to retrieve the schema, receiving 2,978 bytes covering remember, ask_user and recall. Sequence 1364 retries successfully with type=decision.

The validator now includes the existing schema choices on a small enum failure, for example:

`$.type: value is not in enum; allowed values: ["fact","decision","task-log","preference"]`

That error is 133 bytes in the production Loop fixture (65 bytes more than the former result). The information needed to choose a valid type is present without discovering another schema. A real-model comparison was not run for this change; the saved sequence shows the missing information and the actual discovery call, not a guaranteed future turn reduction.

The hint is compiled once per schema. Only complete scalar lists with at most 16 entries, each string/number at most 128 bytes and a serialized list at most 512 bytes are included. Large or structured choices retain the previous short error. No values are guessed, coerced or accepted merely because of the hint. Invalid calls still do not execute.

## Unnecessary processing on successful calls

Inspection of the same validator found canonical serialization at every schema node, even when neither enum nor const comparisons were present. Large patch strings were therefore re-serialized at the root, array, child-object and string nodes before normal checks.

Canonical equality is now computed only at nodes that actually constrain enum or const. Numeric equality, uniqueness, type checks and validation order remain unchanged.

## Measurements

Three 300 ms benchmark samples on Windows / Ryzen 7 5800X3D; medians for JSON decode plus schema validation:

| Valid payload | Time before → after | Allocated bytes before → after | Allocations before → after |
| --- | ---: | ---: | ---: |
| Small search request | 3.491 → 1.810 µs | 2,227 → 1,641 | 71 → 35 |
| Memory note, about 4 KB | 37.801 → 24.396 µs | 39,974 → 20,166 | 50 → 30 |
| Patch with 20 changes | 0.883 → 0.182 ms | 556,015 → 125,423 | 2,495 → 731 |

The patch fixture uses about 79% less validation time and 77% fewer allocated bytes. This is local validation work, not provider latency or end-to-end task time. There are no new model calls, provider-schema bytes or permanent prompt instructions.

## Verification

- Red/green tests cover the saved memory-type failure through Registry.Execute and native/thin Loop requests.
- Small choice lists preserve JSON escaping and number/bool/null types.
- Large and structured enums remain bounded and still validate accepted values correctly.
- Existing enum, const, numeric equality, unique items, combinator, schema-registration and coercion tests pass.
- Full `go test -timeout=90s ./...`, `go vet ./...` and CLI/GUI builds pass.

Raw trace metadata, red/green results and benchmark samples are in `.tmp/enum-repair-2026-09-26/`. No user's memory database was written by the fixtures.

Both validated executables were installed with backups and SHA-256 verification. Restart running instances to load these changes.
