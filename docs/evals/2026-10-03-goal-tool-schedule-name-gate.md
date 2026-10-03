# Early resource-name admission for tool scheduling

Date: 2026-10-03. Baseline: main f580174 / dev15.

## Change and exact scope

`fileAccessesForCall` previously decoded every call's complete arguments into a map before switching on the tool name. Registered tools without a supported file-resource footprint, including `ctx_execute` and arbitrary extensions, immediately returned the existing conservative unknown result after that allocation.

An early name switch now returns the same `nil, false` before parsing those unsupported names. Known footprint cases retain the original JSON decoding, path access, read/write flags and ZIP action checks. Actual dispatch still performs the existing hardening, validation, approval and execution. No cache, extra model call, instruction, schema, provider-history or Zen change is introduced.

Owned baseline `loop_tool_schedule.go` SHA-256:
`3eeead50c2e010cb57b83e16d1cd4a6381e05f572105c551fcbbd4e047198fd1`.
The production branch was byte-identical to this baseline before the port.

## Completed-session scale

A single bounded, read-only metadata query covered the same six completed sessions selected for earlier audits. It exported only labels, tool names, counts and argument byte lengths; no session IDs, prompts or argument contents. Their 579 multi-call batches contained 446 potential first unsupported footprint calls after excluding pure delegation batches. Of these, 267 were `ctx_execute`, with 82,842 total argument bytes and a maximum of 1,273 bytes.

This is exposure metadata, not a historical parse-count measurement. An all-read-only batch may bypass footprint scheduling, and original registry policy, workspace configuration and placeholder rewriting were not replayed. In particular, saved `read` placeholders are rewritten before scheduling; their metadata is not evidence of this overhead in the live Zen path. The large benchmark payloads below are stress controls, not demonstrated incidence in these sessions.

## Matched reachable-path measurements

Windows amd64, AMD Ryzen 7 5800X3D, `GOMAXPROCS=2`, `CGO_ENABLED=0`; three completed serial pairs in B/C, C/B, B/C order, 150 ms per case. Values are medians, not an end-to-end inference timing.

The benchmark calls the production `invokeToolCalls` path reached from `Loop.step`: a registered unsupported-footprint mutation followed by a known read. It includes current hardening, registry schema processing, verification, per-call outcome processing and ordered tool-result history appends. Both tool functions and verifiers are controlled constant fixtures; no command, provider, filesystem read or screenshot is executed. Events are drained and message-slice length reset each iteration.

`size` in the benchmark name is payload bytes. The caller's JSON argument envelopes are 290, 1,307, 32,802 and 131,106 bytes respectively. Thus the 1,273-byte payload case approximates, but does not exactly equal, the historical maximum argument length.

| Registered unsupported tool | Payload | Old µs/op | New µs/op | Old B/op | New B/op | Allocations old → new |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| ctx_execute | 256 B | 16.00 | 12.81 | 9,066 | 7,834 | 104 → 93 |
| ctx_execute | 1,273 B | 40.56 | 31.96 | 19,147 | 15,707 | 105 → 94 |
| extension fixture | 256 B | 10.91 | 8.22 | 7,177 | 5,945 | 82 → 71 |
| extension fixture | 1,273 B | 25.54 | 16.61 | 13,034 | 9,594 | 83 → 72 |
| ctx_execute, stress | 32 KiB | 836.09 | 641.17 | 438,458 | 355,889 | 111 → 99 |
| ctx_execute, stress | 128 KiB | 3,388.72 | 2,366.48 | 1,618,382 | 1,339,229 | 116 → 105 |
| extension fixture, stress | 32 KiB | 480.28 | 293.22 | 290,214 | 207,657 | 88 → 77 |
| extension fixture, stress | 128 KiB | 1,906.06 | 1,150.38 | 1,076,917 | 797,632 | 93 → 81 |

The unsupported-name leaf itself goes from 11 allocations to zero: 2.33 µs / 1,200 B for the small payload and 8.33 µs / 3,440 B for the 1,273-byte payload become approximately 2.8 ns / 0 B. The reachable-path rows provide the more useful bound: about 3–9 µs and 1.2–3.4 KB per controlled small batch, with no resident-cache cost.

There is no measured change to provider input tokens, model turns, prompt processing, generation TPS or application RSS. Savings apply only when an unsupported name reaches footprint scheduling. Existing sequential/concurrent policy remains the same.

## Correctness and maintenance controls

Both completed baseline and prototype runs passed the focused scheduling controls. Durable tests cover:

- Unsupported registered, unknown, empty and case-different names with valid, malformed, null, array, Unicode/NUL and nested large arguments; all keep the unknown barrier.
- Known read/write/copy/move/ZIP footprints, invalid path types/actions and malformed arguments; original flags and untrimmed path text are preserved.
- Public `Loop.Run` with an unsupported mutation followed by a known read: exact raw-argument hashes, execution order, tool-call IDs, result evidence and two provider requests.
- An AST admission guard comparing the early switch's name set against the actual footprint switch. Adding a new supported footprint cannot silently leave the gate outdated.
- Existing alias, independent-write, ZIP, mixed-batch and missing-workspace controls remain part of the scoped check.

The duplicate supported-name list is the maintenance risk addressed by the admission guard. The guard is intentionally narrow and will need updating if the function's switch structure is refactored.

Final production-port checks: `gofmt` on the two owned Go files; `go test ./internal/agent -run "Test(FileFootprint|ScheduleGate|FileBatch|ToolZip|ReadCalls|MixedTool)" -count=1` PASS (0.169 s); `go vet ./internal/agent` PASS. The formatted production source is byte-identical to the approved prototype plus its comment. No benchmark or live model call was repeated after the port.

## Reproduction receipts

Completed raw benchmark outputs, parity results, redacted metadata, original source/prototype and median JSON are under ignored `.tmp/goal-runtime-round13-2026-10-03`. The original overlay maps describe the pre-port fixture. Post-port replay overlay maps replace the durable test file with that exact measured fixture and select either original or prototype source, so they do not compile duplicate test names.

With portable caches and Go environment from the directory's runner, replay either arm with:

```text
go test -overlay=.tmp/goal-runtime-round13-2026-10-03/bench-replay-baseline.overlay.json ./internal/agent -run xNoGateTestsx -bench "BenchmarkScheduleGate(InvokeBatch|Leaf)$" -benchmem -benchtime=150ms -count=1
go test -overlay=.tmp/goal-runtime-round13-2026-10-03/bench-replay-candidate.overlay.json ./internal/agent -run xNoGateTestsx -bench "BenchmarkScheduleGate(InvokeBatch|Leaf)$" -benchmem -benchtime=150ms -count=1
```

The accepted port adds only a comment to the measured early guard; its algorithm and measured caller fixture are unchanged. Finished measurements were reused instead of rerunning them.
