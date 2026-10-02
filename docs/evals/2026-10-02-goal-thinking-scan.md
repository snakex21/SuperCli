# Reasoning marker probe: avoid whole-answer lowercase copies

Date: 2026-10-02. Scope: backend text processing only. No provider calls, GPU work, new prompt instructions, token-policy changes, or OpenCode Zen changes.

## Finding and change

`captureThinking` previously called `strings.ToLower` on the entire answer before checking whether a reasoning-tag prefix existed. For mixed-case code and ordinary answers, that allocated a second copy even when the answer had no reasoning. The same helper is used by `hasVisibleUserReply` when finding the latest completed answer in model-history projections.

The new probe searches for `<`, compares the three existing ASCII prefixes without allocation, and uses the existing Unicode lowercase semantics only on a bounded possible prefix. It deliberately does not substitute `EqualFold`: dotted I and Kelvin K distinguish those semantics. A 48-byte source span covers the longest 11-rune prefix even with four-byte UTF-8 input. The actual regex matching, captured blocks, whitespace handling and byte-indexed removal are unchanged.

No cache is added. There is no retained-memory growth or cache invalidation state. Transcript and provider payload bytes remain unchanged.

## Measurements

Windows amd64, Ryzen 7 5800X3D, Go benchmark, GOMAXPROCS=2, 200 ms per case. Figures measure this synthetic CPU path and allocated bytes, not total GUI RSS, model prefill or network latency.

| Case | Previous time | New time | Previous bytes/op | New bytes/op | Allocations/op |
|---|---:|---:|---:|---:|---:|
| Ordinary mixed-case answer, 8,960 bytes | 15.180 us | 0.094 us | 9,472 | 0 | 1 -> 0 |
| Ordinary mixed-case answer, 288,000 bytes | 471.155 us | 3.030 us | 294,912 | 0 | 1 -> 0 |
| HTML without reasoning, 9,900 bytes | 34.615 us | 8.613 us | 10,240 | 0 | 1 -> 0 |
| Actual tagged reasoning | 213.881 us | 200.738 us | 12,699 | 6,518 | 5 -> 4 |

The existing `BenchmarkResolvedProjectionScan/completed_800` measured the actual tool-history projection path: **51.262 us / 113,024 bytes / 4 allocations -> 28.155 us / 99,200 bytes / 2 allocations**. Approximately 45% less CPU time for that fixture; this is not a claim that a whole model turn is 45% faster.

The unchanged baseline was measured before source edits through a Go overlay under `.tmp/runtime-backend-2026-10-02-round2`. Benchmark fixtures and raw results remain there. The checked-in helper benchmark preserves the previous implementation as its differential reference.

## Validation and risks

- Existing reasoning, resumed-history, retained-thinking, prepared-request estimate and resolved-projection tests passed in the scoped run.
- Differential probe/output tests cover 3,000 generated Unicode/case/noise inputs plus explicit closed, open, partial, malformed and mismatched tags, non-ASCII text before tags and invalid UTF-8 bytes.
- Dotted I/Kelvin K cases preserve the previous distinction between the lowercase probe and regex case folding.
- Ordinary mixed-case, Unicode and HTML answers without markers allocate zero bytes in the scan.
- No actual screenshot/model workload was repeated. Real tagged reasoning still pays the existing regex cost; that is not this change's target.

A second candidate, skipping `DormantImages` copies when no image exists, saved only 48 bytes and roughly 33 ns per call. It was not implemented: its smaller benefit did not justify extending the writer aliasing surface in this round.

## Files

- `internal/agent/stream_strip_thinking.go`
- `internal/agent/stream_thinking_scan_test.go`

This round did not stage, commit, build EXEs, publish or change session data. Integration checks are performed by the parent task.
