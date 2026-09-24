# Less repeated CPU and allocation work before model requests (2026-09-23)

## Change

A CPU profile of the warm context-preparation fixture attributed about 24% of
sampled CPU time to nonWhitespaceLen. The coordinator also materialized both
schema and catalog tool slices whenever it needed only the wire definitions.

The byte estimator now uses standard-library byte counts for strings of at least
64 bytes. Short strings retain the scalar loop: measured call overhead made four
counts slower on 8-byte names. It still excludes exactly space, tab, CR and LF;
Unicode whitespace and malformed UTF-8 retain their previous byte semantics.
This is not a tokenizer change or a recalibration of compaction thresholds.

The coordinator builds tool definitions directly from the visible snapshot,
without constructing and discarding the catalog tail. A shared selection
predicate preserves native/thin, stable/dynamic and orchestrator policy, including
the existing Word-tool promotion exception. Each call owns its output slice.
The actual catalog still uses the same partition policy.

No cache, new dependency, helper inference, prompt instruction or provider-specific
branch was added. GUI/TUI and local/cloud requests use the same implementation.
Schemas, tool order and estimated token counts remain unchanged. The OpenCode
Zen transport was not edited.

## Paired measurement

Windows amd64, Ryzen 7 5800X3D. Independent before/after test executables were
built from the same workspace with a Go overlay containing the two original
production files. No production sources had to be swapped during the comparison.

Runs were sequential in before/after, after/before, before/after order, with
2000 iterations per variant. The table gives medians of the three runs. Each
iteration performs two budget estimates, tool-definition assembly, provider-view
assembly and a final request estimate. This models the repeated local preparation
work around a loop step; it excludes inference, network I/O and request encoding.

The long fixture has 97,460 locally estimated history tokens in both builds.
It uses 40 assistant code messages, user messages and a registry with core tools
plus 48 extension tools. The shorter fixture uses shorter assistant findings.
Fixtures do not represent every provider, history shape or hardware platform.

| Fixture | Before | After | Allocated bytes before/after | Allocations before/after |
|---|---:|---:|---:|---:|
| Shorter, native tools | 70.654 us | 32.794 us | 88,397 / 43,556 | 59 / 20 |
| Shorter, thin tools | 86.094 us | 24.504 us | 84,569 / 50,101 | 70 / 22 |
| Long, native tools | 296.258 us | 54.155 us | 88,405 / 43,553 | 59 / 20 |
| Long, thin tools | 366.834 us | 48.456 us | 84,574 / 50,106 | 70 / 22 |

The long fixture's measured preparation cost fell by about 5.5–7.6 times;
allocated bytes fell by 41–51%. These are small absolute CPU savings repeated
per preparation cycle, not a corresponding improvement in overall answer time.
Model token use and provider charges do not change as a result of this patch.

Initial scanner and context measurements ran concurrently for exploration.
They are retained for transparency but are not the source of the paired results.

## Validation

- Byte-count parity with the prior scalar algorithm: all byte values, Unicode,
  invalid UTF-8, whitespace, short lengths, and longer texts at varied offsets.
- Definition parity against the previous builder across eight combinations of
  thin/native, stable/dynamic and orchestrator modes, before and after activation,
  deactivation and reset; byte-identical serialized definitions.
- Prior request snapshots cannot be mutated by later assembly. Empty definitions
  stay nil; final-reply-only requests still carry no tools.
- Existing context, reasoning, cache-prefix, route and provider tests pass.
- Full go test ./... and go vet ./... passed.

Reproduction:

    go test ./internal/llm ./internal/agent
    go test ./internal/agent -run '^$' -bench '^BenchmarkContextPreparation(Warm|Long)$' -benchtime=2000x -count=3
    go test ./internal/llm -run '^TestNonWhitespaceByteParity$' -bench '^BenchmarkNonWhitespaceScan$' -benchtime=100ms

App-local raw evidence: .tmp/request-estimate/context-paired.json,
before-sources.json, before-overlay.json, before.cpu, profile-before.txt,
scan-comparison.json, context-before.json, focused.json, test-all.json and
vet-all.json. The before/after benchmark executables are retained alongside them.
