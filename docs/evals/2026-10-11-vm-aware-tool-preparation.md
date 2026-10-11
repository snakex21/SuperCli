# Tool preparation measurement with concurrent VM work

## Scope and resource conditions

The user requested another measured, universal efficiency pass while another
agent was working on a VM. No VM process, model setting, thinking level, prompt,
history retention rule or provider configuration was changed. Test/build processes
used `GOMAXPROCS=1` and Go `-p 1`; application defaults were not reduced.
Temporary files, module/build caches, receipts and binaries stayed in the
application tree.

Two resource observations bound the investigation, rather than sampling a running
operation for progress:

| UTC observation | Available physical memory | Total physical memory |
| --- | ---: | ---: |
| 2026-10-10 22:55:19.3954169 | 971,726,848 bytes (0.91 GiB) | 34,280,775,680 bytes |
| 2026-10-10 23:10:06.5763226 | 20,430,163,968 bytes (19.03 GiB) | 34,280,775,680 bytes |

The first observation also found a local model server with approximately 13.52
GiB working set. These observations establish changing machine conditions; they
do not identify the owner of all memory use or measure memory at every benchmark
sample. No inference or additional WPR capture was started in this pass.

## Compaction and preflight boundary

The existing archived first-compaction coding receipt supplies a 13-message,
25,996-byte transcript. `TestFirstCompactionPreparationBoundary` passed: one
text-only provider callback, no main tool schemas, byte-identical input and
unchanged canonical history. The callback deliberately stops before inference.

Five 150 ms benchmark samples measured local preparation:

| Operation | Median | Range | Allocations |
| --- | ---: | ---: | ---: |
| Recorded transcript render | 6.694 us | 6.650–7.085 us | 11 |
| Post-helper exact facts | 25.860 us | 22.209–26.898 us | 143 |
| `CompactNow` to `Complete`, no main tool schemas | 8.643 us | 7.731–12.026 us | 23 |

This is neither a summary-generation measurement nor a before/after compaction
speedup. Local preparation cannot explain the historical 116-second backend gap.
The earlier [cache allocation/paging investigation](2026-10-10-cache-allocation-paging.md)
remains the evidence for that gap; the slow outlier was not reproduced here.
Preflight's existing fresh-status and bounded-context behavior was reviewed and
left intact. No new inference, preflight or agreement-preservation claim follows
from a microbenchmark of tool dispatch.

## Eliminating duplicate preparation

The loop previously called `Registry.PrepareArgs` to form a reuse/verified-effect
key, then called `Registry.Execute` on a miss. `Execute` prepared the same arguments
again. The accepted tool contract is append-only, so this duplicate coercion,
validation and pure repair can be avoided within a single invocation.

`PreparedInvocation` privately binds the originating registry, tool name and an
immutable copy of accepted arguments. `ExecutePrepared` resolves the registered
callback again and supplies a separate mutable argument buffer. It bypasses only
the repeated preparation, not live permissions, checkpoint wrappers, current
context or execution errors. Tokens are local to an invocation, not retained in
the result cache. Ordinary tools without reuse metadata keep the existing
`Execute` path. No model, website or download destination is special-cased.

Verified replay receives a separate input copy: a callback that declines after
mutating its input cannot change execution or verification arguments. A fresh
cancellation check before dispatch also prevents such a callback from cancelling
the turn and then starting a tool that ignores cancellation. A failed argument
preparation keeps the existing ordinary error handling.

## Interleaved comparison

Both binaries use the same benchmark and production schemas, with deterministic
callbacks replacing network/disk work. The baseline overlays only the frozen
`loop_tools.go` from commit `5d86717630dc96e9d3a8549a0afa0f4a51737b26`.
Five pairs alternated AB/BA order, 150 ms per case, one Go worker. The changed
source hashes were identical throughout the successful v3 run.

| Case | Before median (us) | After median (us) | Before/after allocs | Before/after B/op | Actual callback calls/op |
| --- | ---: | ---: | ---: | ---: | ---: |
| Ordinary tool | 4.376 | 4.456 | 55 / 55 | 2,784 / 2,784 | 1 |
| Small observation, miss | 9.554 | 8.028 | 105 / 85 | 8,864 / 6,984 | 1 |
| Small observation, hit | 7.917 | 8.201 | 77 / 78 | 6,328 / 6,353 | 0 |
| Filtered observation, miss | 33.094 | 26.480 | 322 / 228 | 14,450 / 11,114 | 1 |
| Explicit refresh | 21.542 | 16.749 | 217 / 153 | 11,825 / 8,921 | 1 |
| Filtered observation, hit | 23.953 | 24.057 | 220 / 221 | 10,257 / 10,386 | 0 |
| Verified effect, miss | 21.938 | 15.813 | 221 / 152 | 12,177 / 9,209 | 1 |
| Verified effect, replay | 13.996 | 14.273 | 138 / 140 | 7,785 / 7,977 | 0 |

All five paired miss/refresh measurements favored the candidate. Allocation
reductions were repeatable, with at most one byte of measurement rounding. Timing
ranges overlap for ordinary tools and observation hits; their latency improvement
is not established. Verified replay has a small measured overhead and two extra
allocations for immutable ownership and replay isolation. That tradeoff is
explicit: misses save 20–94 allocations while live replay retains its safety
boundary. This is a small local optimization, not a 16–28% faster overall agent.
Provider calls, model thinking and network request counts were not reduced by
this change. Cache-hit/replay cases still dispatch zero execution callbacks;
refreshes and distinct requested outputs still execute.

Private reproducibility receipts: `.tmp/efficiency-vm-aware-2026-10-11/` contains
frozen sources, the overlay, source hashes, complete paired v3 logs, parsed raw
samples and JSON summaries. Failed prototype compilation/assertion logs remain
separately labelled; they are not timing results.

## Regression coverage

The frozen baseline fails the new single-preparation, cancellation, registry
identity and replay-input mutation regressions. The candidate passes all five
focused agent tests. Core API tests cover accepted-byte/error parity with ordinary
execution, pure repair, nil/empty arguments, coercion, foreign/zero tokens,
caller/replay/callback buffer ownership, unrelated registrations, visibility,
current permission/context/error behavior and concurrent independent invocations.
Final native validation passed for `internal/tools/core`, `internal/tools`,
`internal/tools/web` and `internal/agent` (38.420 s total command wall time),
including the archived first-compaction boundary, mixed-work/two-output reuse,
download replay/authorization and batch/cancellation regressions. `go vet` on
these packages passed (6.196 s). Both native Windows binaries built successfully;
CLI `--version` and GUI `--help` checks passed. The GUI does not implement
`--version`; the unsuccessful flag probe is preserved as an unsupported CLI flag,
not treated as a failing application test.

Both application binaries were replaced using checked destination/source paths,
atomic file replacement, rollback on failure and SHA256-verified backups under
the same portable artifact directory. No running SuperCli process was terminated.
These are local changes; no additional commit, tag or release was published.
