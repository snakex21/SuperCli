# One scan for repeated files inside read_many

Date: 2026-09-26. Shared local/cloud file tool; no provider/Zen or prompt changes.

## Evidence

The fixed saved session contains 35 read_many calls with multiple ranges of the same file. Previously each range started its own goroutine, resolved/opened the file and scanned the prefix independently. Repeated/overlapping ranges therefore consumed the same bytes several times inside one already-batched tool call.

The worker handoff paths were inspected first. No additional handoff change was made without evidence of a defect; this change addresses the confirmed redundant file scanning used by both main agents and workers.

## Implementation

Group valid requests by their exact requested file path. Different files still execute concurrently; a repeated file is resolved/opened once and uses the existing bounded multi-range reader. Results retain request order, independent range validation, CRLF normalization, errors and all output/range limits. The singleton path keeps its direct read.

The multi-range reader now has an EOF-aware variant. EOF belongs only to spans reaching the observed end, not earlier spans from the same file. Existing search consumers keep their previous non-EOF variant. Overlapping result slices remain independent, with shared immutable line strings.

Grouping lasts for one invocation. Every subsequent call opens current file contents. Distinct aliases or hard links are not deduplicated. This is not a filesystem cache or an atomic snapshot guarantee under concurrent external writes.

## Measurements

Windows, Ryzen 7 5800X3D; three 300 ms benchmark samples per scenario. Table entries are medians:

| Scenario | Before | After | Allocated bytes before → after |
| --- | ---: | ---: | ---: |
| Four nearby ranges of one file | 1.286 ms | 1.020 ms | 1,067,885 → 983,396 |
| Four ranges around line 40,000 | 2.023 ms | 1.979 ms | 1,083,897 → 999,330 |
| Four different small files (control) | 1.610 ms | 1.470 ms | 1,064,458 → 1,064,622 |
| One small file (control) | 0.767 ms | 0.761 ms | 214,089 → 214,178 |

The nearby repeated-file fixture takes about 20.6% less local tool time and allocates about 7.9% fewer bytes. The distant fixture shows only a small time difference despite reduced scanning, because the previous independent reads ran in parallel. Control timing differences should be treated as noise, not an algorithmic speedup. Group bookkeeping adds a few allocations for unique-file cases.

A counted-reader replay of the distant ranges consumes 6,715,631 bytes independently versus 1,703,477 bytes as a merged scan (about 74.6% fewer). This measures bytes delivered to the buffered reader, not physical disk transfers; the OS may cache reads.

No reduction in model turns, prompt tokens or provider latency is claimed for this change.

## Verification

- EOF-aware grouped results match independent reads across overlaps, request order, EOF, invalid spans, binary/legacy text, CRLF, Unicode and huge lines.
- A bounded-reader test confirms reading stops at the final requested window rather than scanning the remaining file.
- Tool tests preserve section ordering, partial errors, cancellation and freshness after an intervening edit.
- Previous range-recovery, output-budget and native/thin tests pass.
- Full `go test -timeout=90s ./...`, `go vet ./...` and both executable builds pass.

Raw benchmark samples, derived metrics, fixed-snapshot counts, tests and installation hashes are in `.tmp/grouped-read-2026-09-26/`.

Validated CLI/GUI executables were installed with previous binaries backed up and SHA-256 verified. Restart existing instances to load the optimization.
