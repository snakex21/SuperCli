# Memory storage and context audit — 2026-10-08

The read-only audit of the application's global memory database and 25 project
memory databases found 276 entries containing 233,667 source bytes. The database
files occupy 3,559,424 bytes in total; the Markdown mirrors occupy 251,439 bytes.
No entry exceeds 16 KiB and no stored entry contains a detected data URL or long
base64 payload. Memory is therefore not the source of the reported multi-GB
checkpoint storage growth.

The audit found eight extra exact duplicate rows, twelve legacy classifier
fallback patterns and one legacy task journal containing malformed UTF-8.
Existing recall filters already exclude the classifier fallback patterns. The
audit did not change or delete any saved memories.

## Changes

- `reflect.Store.List` now filters useful pattern scopes, orders by their saved
  confidence and applies the requested limit in SQLite. Previously it read every
  row and its full content before filtering. The injector's read remains capped
  at 50 candidate patterns.
- The rendered pattern section has a 2,048-byte ceiling, including its wrapper;
  titles and descriptions are capped independently. Multi-line legacy titles
  become one bullet and truncation preserves UTF-8.
- `RecentBudgeted` counts rendered IDs and separators, not only source content.
  The briefing reserves its closing wrapper, emits a section label together with
  its first fitting note and continues past oversized notes to smaller useful
  notes. Its configured cap remains the hard limit under the package's byte/4
  token estimate.
- Raw emergency tails are sliced at character boundaries. Windows path tokens
  normalize into the same learned-error bucket just like POSIX paths, preventing
  repeated path-specific variants of the same error.

## Validation

Read-only SQLite evaluation over all 26 saved memory databases confirmed that
the new filtered query returns the same 20 useful patterns in the same confidence
order for limits 1, 3, 50 and unlimited. No stored content was emitted by this
evaluation.

Added regression cases cover tiny hard budgets, long IDs, oversized recent
preferences hiding shorter facts, UTF-8 tail cuts, Windows error-path grouping,
filtering before pattern limits and the complete pattern-section ceiling. The
mixed-store benchmark covers 4,000 unrelated entries plus 50 patterns. It now
checks that both implementations return the same three IDs before measuring
`legacy` (read all entries, filter and rank in Go, then limit) and `filtered`
(filter, rank and limit in SQLite) subbenchmarks. Both measure retrieval and
selection of entries; neither includes the common final conversion into
`reflect.Pattern` or rendering. The baseline follows the previous
`reflect.Store.List` selection algorithm from HEAD and uses synthetic data only.

The coordinating agent confirmed that the `storage/memory` and `reflect` test
packages passed. Before adding the comparable baseline, the filtered mixed-store
benchmark measured approximately 164 microseconds/op, 3,640 B/op and 82
allocations/op. The new paired benchmark has not yet been run; no relative RAM or
speed improvement is claimed until both results are measured together.

`gofmt` and `git diff --check` completed successfully for the implementation.
The baseline-only follow-up runs `gofmt`; all Go test/build and benchmark
execution remains with the coordinating agent. These changes constrain memory
context and read allocations; no measured reduction in complete model turns or
provider token counts is claimed.

Paired benchmark on Windows/amd64, Go 1.26.2, Ryzen 7 5800X3D, GOMAXPROCS=2 (4050-entry synthetic store; same three IDs verified before timing):

| Query | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| Legacy all-row scan/filter | 11,576,035 | 9,527,930 | 93,724 |
| SQL-filtered retrieval | 158,769 | 3,640 | 82 |

This is about 73 times faster for this fixture and approximately 2600 times fewer allocated bytes per query. It measures the retrieval/selection operation, not total process RAM or model throughput.
