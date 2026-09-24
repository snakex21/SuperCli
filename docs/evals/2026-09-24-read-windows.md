# Read search context in one pass — 2026-09-24

## Shipped change

search_code with context used to reopen and rescan a file's prefix for every
separate neighborhood of matches. Twenty distant matches meant twenty context
reads after the initial search scan. It now reads those neighborhoods from one
open file and one forward pass, retaining only the selected lines.

The shared file reader prepares one buffer and encoding check, then continues
through sorted/merged ranges without resetting that buffer. The ordinary
single-range scan loop is reused. Selected ranges retain their original order,
line numbers and independent errors; overlapping results have independent slices.
Later calls reopen the file, so this is not a persistent cache.

Search still performs its initial content scan. This optimizes the subsequent
context reads, not every part of the search. The 500-line context budget,
per-line truncation, source ordering and match markers remain in place.
No tool schema, system instruction, provider adapter or extra model call changed.
The implementation is shared by CLI/TUI/GUI and local/cloud providers. Special
Zen transport/protocol is untouched.

## Measurements

Windows / Ryzen 7 5800X3D. Five samples of 200 ms per final benchmark case.
The fixture has 100,000 lines, mostly 80 bytes each, with 20 distant matches.
Context radius is 2. The initial and final fragments are handled exactly as before.
Benchmark setup and fixture creation are outside the timed loop.

| Median per operation | Before | Final |
|---|---:|---:|
| Context rendering only | 26.020 ms | 2.526 ms |
| Search + context, Go fallback | 30.940 ms | 7.960 ms |
| Context-rendering allocation bytes | 732,237 | 112,924 |
| Search + context allocation bytes | 883,078 | 228,587 |

The whole tool is about 3.9x faster in this workload and allocates about 74% fewer
bytes. These are allocated bytes per operation, not peak RAM or resident memory.
This is a favorable multi-match fixture, not an end-to-end model latency result.

Single-range control on a separate 100,000-line / 16 MB file:

| Range position | Before median | Final median |
|---|---:|---:|
| Early | 41.693 us | 44.402 us |
| Middle | 1.789 ms | 1.764 ms |
| Late | 3.543 ms | 3.576 ms |

Allocation count stays 31 per operation in all single-range cases. The early
measurement is about 2.7 us slower; there is no claim that every read speeds up.
The final scan loop performs no multi-range checks per input line.

## Rejected read_many experiment

Inspection found one repeated-file batch among 154 recent saved read_many calls.
Batching those ranges was tested as another consumer of the shared reader.
The initial implementation accidentally serialized filesystem path checks,
raising the same-file benchmark from about 2.68 to 5.82 ms and also slowing
distinct-file batches. Restoring parallel checks removed that large regression.

A later grouped implementation reduced the same-file case to about 2.05 ms and
156 KB allocated, but repeated measurements still showed roughly 5–8% higher
latency for the different-file case. The grouping change was therefore removed.
Production read_many.go is byte-for-byte the version from the start of this turn.
Its discarded source, benchmark and test are saved only under .tmp/read-windows.
The shared scanner changes are still exercised by its existing tests.

## Correctness and validation

- Multi-range results compared against independent ReadLinesBounded calls for
  empty files, CRLF, UTF-8, legacy bytes, binary input, huge lines, overlaps,
  invalid ranges, line truncation and EOF.
- Cancellation during a large skipped line stops promptly. An 8 MiB gap is
  consumed without being retained; reads stop after the requested endpoint,
  allowing the existing 32 KiB buffer's bounded read-ahead.
- Previously completed ranges survive a later I/O error. Subsequent calls see
  changed file content, and overlapping output slices do not alias.
- Search tests cover separated windows, the 500-line budget, missing files,
  cancellation, backend equivalence and preservation of default behavior.
- A before/final replay of read_many, read_lines and search_code, including partial
  errors and long UTF-8 lines, produced identical 10,307-byte serialized results:
  f2285d6082414cd1b594499851684d9e89efda3cb4c83788f9b952e5f052d597.
  Text, retained output, model preview and error strings were compared.
- go test ./..., go vet ./..., CLI and GUI builds passed.

No live model rerun was needed for this implementation-only change: the controlled
tool outputs and definitions are unchanged, so the claim is about local tool cost,
not reduced reasoning or fewer provider turns.

## Reproduce

Run:
go test ./internal/tools/search -run ^$ -bench ^BenchmarkSearchSeparatedContext$
-benchmem -benchtime=200ms -count=5

Single-range control:
go test ./internal/tools/fileops -run ^$
-bench ^BenchmarkReadRangeStreaming$/(early|middle|late)/stream$
-benchmem -benchtime=200ms -count=5

Before-source snapshots and overlay, raw benchmark output, rejected variants,
the offline replay harness and final verification outputs are in .tmp/read-windows.
The baseline overlay restores the exact original scanner and tool sources.
A baseline-only compatibility adapter lets added types compile; the original tool
implementations do not invoke it.
