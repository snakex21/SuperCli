# One fresh UI preference snapshot per loop preparation — 2026-10-03

Baseline: 78c5366 / dev16. Scope: the GUI loop's current/legacy auto-memory preference lookup. The TUI and provider transports are unchanged.

## Evidence and change

The GUI used uiSettingBool(current, uiSettingBool(legacy, true)). Go evaluates the fallback argument eagerly, so this read and decoded webgui-settings.json twice even when the current key was present. Each lookup separately acquired the existing settings mutex. A bounded read-only metadata inspection found portable settings blobs of 4,134 bytes / 9 keys and 27,513 bytes / 17 keys; no values or key contents were exported.

The existing helper now accepts optional fallback keys, resolves all keys from one freshly loaded map under the same existing mutex, and prefers the current boolean. Non-boolean/missing current values fall through to the legacy boolean, then to the existing default. There is no persistent map/cache, watcher, new model request, prompt or tool-schema change. Subsequent loop preparations still load saved preferences anew. Coordinated settings writes cannot interleave between two preference snapshots anymore; uncoordinated external replacement during a lookup is intentionally observed as one snapshot rather than two potentially inconsistent versions.

## Matched measurements

Windows amd64, Ryzen 7 5800X3D, Go 1.26.2, GOMAXPROCS=2, CGO disabled and portable caches. A-B-B-A with 200 ms per case. All four processes completed with PASS. The synthetic settings have 17 benign keys and the same exact byte lengths as the observed sizes; no real preference content is used.

The reachable newLoopWithSession benchmark includes real loop/registry construction, goal refresh and memory/context setup using an owned echo Engine and portable synthetic workspace/data. It performs no provider POST, tools, commands, screenshot or GPU inference. An initial loop primes only that fixture Engine's lazy stores before measurement. Go B/op is transient allocation, not retained heap or native RSS.

| Complete loop preparation | Baseline B/op | Candidate B/op | Baseline time | Candidate time |
| --- | ---: | ---: | ---: | ---: |
| Settings absent | 823,154–826,874 | 824,053–824,786 | 3.205–3.546 ms | 3.481–3.551 ms |
| 4,134-byte synthetic settings | 850,703–851,118 | 837,880–840,078 | 3.389–4.050 ms | 3.563–3.695 ms |
| 27,513-byte synthetic settings | 944,314–948,765 | 887,430–890,354 | 3.650–4.108 ms | 3.717–3.957 ms |

Whole-loop times overlap. The useful result is about 11–13 KB / 54–61 KB fewer allocated bytes in the two present-file cases. No absent-file allocation gain is claimed. The smaller preference lookup alone removes one read/decode: 4,134 bytes 25,970–26,034 → 13,017 B/op and 161.8–183.0 → 89.5–90.4 µs; 27,513 bytes 119,995 → 59,997 B/op and 438.1–480.5 → 246.0–248.3 µs. Lookup allocations halve from 164 to 82. The complete loop remains the primary bound; this does not establish a model prefill/TPS, token/turn, application RSS or universal latency improvement.

## Correctness

Both original and candidate passed the same fourteen cases on one live Engine with saved settings replaced between calls: default/missing, current and legacy booleans, conflicting values, null/string/number/array values, malformed JSON, null/array root and a final fresh preference change. Each real loop prompt contains exactly the expected MemoryGuidance, not the opposite policy. Existing full-prompt and memory-guidance regressions remain unchanged. The port adds a comment and semantic durable test/benchmark names to the measured algorithm.

Final formatting/scoped checks: gofmt of the three owned Go files, full internal/webgui test suite and package vet completed with exit 0. Integrated tests/builds/install are recorded separately.

Ignored portable evidence: .tmp/goal-root-round14-2026-10-03/settings-snapshot. Original measured overlay maps are retained. Post-port overlay-replay-{baseline,candidate}.json map the durable test file to the same measured fixture, so the baseline compiles without the new optional helper argument.
