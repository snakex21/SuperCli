# Default tool discovery: avoid weak, redundant matches

Date: 2026-09-26. Shared discovery code; no provider, Zen routing, or prompt changes.

## Evidence

A real Muse thin-worker coding run searched for `patch file editing`. Its lexical results contained patch_file and read_context (two matching query words each), plus edit_docx (only the word "file"). The Word tool was activated despite the coding intent. Its schema then remained in subsequent requests.

The source trace is `.tmp/worker-coding-2026-09-26/muse-thin-read-fix/deferred.json`. That run also encountered an overly restrictive test-harness command gate, so its total latency and completion are not used to claim an improvement here.

## Change

For default lexical fallback discovery, examine up to the normal maximum candidate count before applying the default result limit. Drop a result only when its matched query words form a strict subset of the words matched by an earlier, stronger result. Preserve equal coverage and complementary matches.

Explicit positive limits, exact tool-name lookup and nonempty FTS results retain their existing behavior. Single-term queries retain alternatives. Optional tools remain registered and can still be discovered directly. A weak result hidden by this policy may require a more precise query or an explicit limit; this is a ranking tradeoff, not a semantic intent classifier.

No tool is automatically executed, no extra model call is introduced and no instructions are added.

## Verification and measurements

The production worker fixture reproduces the live query with actual tool definitions in native and thin mode:

| Measure | Before | After |
| --- | ---: | ---: |
| Discovery result bytes | 7,916 | 1,957 |
| Native definition bytes before/after discovery | 5,041 → 11,926 | 5,041 → 5,041 |
| Thin definition bytes before/after discovery | 5,284 → 12,169 | 5,284 → 5,284 |

This removes 5,959 result bytes (about 75%) and avoids activating 6,885 definition bytes for later requests in this fixture. Bytes are serialized JSON sizes, not tokenizer measurements. Actual provider latency was not measured for this change.

The worker regression failed before the production change and passed afterward. It also confirms that a later exact Word lookup works and does not activate tools in the parent registry. Discovery tests cover tied matches, complementary intents, single-term breadth, explicit limits and exact lookup, with and without an FTS index.

A 300 ms synthetic benchmark compares the prior explicit three-result path with default focused fallback:

| Catalog size | Prior breadth | Focused default | Added CPU time |
| --- | ---: | ---: | ---: |
| 64 tools | 70,525 ns/op | 75,948 ns/op | about 5.4 µs |
| 512 tools | 474,426 ns/op | 495,489 ns/op | about 21.1 µs |

The focused path adds about 1.8 KB and 14 allocations per discovery in this benchmark. This small local cost is explicit; no universal wall-clock speedup is claimed.

`go test -timeout=90s ./...`, `go vet ./...`, and both CLI/GUI builds pass. Raw red/green, benchmark and validation results are under `.tmp/discovery-focus-2026-09-26/`.

## Scope

The change targets unnecessary schema activation exposed by a real trace. It does not suppress repeated agent reads, change tool execution permissions or alter FTS ranking. Further investigation of redundant reads remains part of the active efficiency goal.

Both validated executables were installed beside the portable application data. Previous binaries and SHA-256 verification are recorded in `.tmp/discovery-focus-2026-09-26/installed.json`. Restart running instances to load the change.
