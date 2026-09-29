# Skip inapplicable argument coercion

Date: 2026-09-30

## Change

Registry execution decoded the entire argument object before every schema check to look for stringified scalar/argv values. For text-only tools such as create_file and remember, no declared field can use that conversion, so this duplicated work on large text payloads. Already typed numeric and argv fields also allocated JSON type errors during the attempted string decode.

Build an immutable list of eligible top-level coercion fields when registering the schema. Skip the coercion decode when that list is empty; inspect only quoted values for eligible fields. Full schema validation still runs on every execution. Stringified scalar/argv repairs, union-type handling, nested-field behavior and raw payload preservation keep their existing contracts.

The registry is shared by GUI, TUI and workers. This introduces no prompt content or provider-specific path.

## Measurements

Windows amd64, AMD Ryzen 7 5800X3D, Go 1.26.2. Median of three runs per version, 200 ms per benchmark. Both versions use the same fixture.

The benchmark measures Registry.Execute argument preparation and validation with an in-process no-op tool. It excludes model generation, file I/O and the real tool implementation. The create-file fixture contains 33,600 bytes of source text; the memory fixture contains 4,140 bytes of note text.

| Fixture | Before (µs/op) | After (µs/op) | Bytes/op before → after | Allocations/op before → after |
| --- | ---: | ---: | ---: | ---: |
| create_file_32k | 369.325 | 262.558 | 253384 → 211776 | 36 → 24 |
| memory_note_4k | 44.484 | 35.941 | 25714 → 20165 | 45 → 30 |
| typed_read | 4.985 | 4.598 | 3138 → 2657 | 77 → 71 |
| stringified_read | 6.162 | 6.222 | 3309 → 3311 | 87 → 87 |
| typed_argv | 5.989 | 4.850 | 3164 → 2683 | 71 → 64 |
| stringified_argv | 8.059 | 8.068 | 3731 → 3730 | 90 → 90 |

The create-file preparation median decreased by approximately 29%. Stringified read/argv timing was essentially unchanged within the variation of these short runs. These figures describe this stage only; they are not an end-to-end CLI or inference speed claim.

Reproduce with:

```powershell
go test ./internal/tools/core -run "^$" -bench "^BenchmarkRegistryArgumentPreparation$" -benchtime=200ms -count=3
```

## Validation

- Explicit registry tests verify exact payload preservation for text, typed scalars and argv, and unchanged conversion for supported stringified values.
- Unknown fields, malformed JSON, invalid types, non-finite numbers and unsupported stringified values for union or nested fields remain rejected before the tool implementation runs.
- Full project suite passed: `go test ./... -timeout=90s`.
- Static analysis passed: `go vet ./internal/tools/core`.
- Complete core tool suite passed with `go test -race ./internal/tools/core -count=1 -timeout=60s`, including concurrent copied registries.
