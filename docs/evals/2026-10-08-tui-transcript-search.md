# TUI transcript search: reuse the filter and stream cold matches

`internal/ui/tui` previously normalized every complete message on every Ctrl+F
menu redraw, even when only the selected row changed. The search result also
retained each matching message's complete normalized text before rendering
shortened the preview to terminal width.

The search cache now holds one filter's ordered message indices, roles and
detached previews of at most 512 runes plus an ellipsis. Matching preserves
the original full-message `Fields` / `Join` / `ToLower` behavior. Preview
truncation occurs after matching, so a phrase at the end of a long message
still contributes the same result and count. There is no per-message
normalized-text cache or second transcript copy.

Value-receiver menu redraws and cursor movement share immutable results.
Message append/removal, completed stream flush and fold/unfold invalidate the
cache revision. A changed normalized filter or replacement message slice is
also detected. Loading a session creates an independent cache. Clearing the
message slice cannot return the prior result. Unfinished streaming text remains
outside search, preserving the original completed-message-only behavior.

## Metadata used to size the fixture

Read-only aggregate queries on the investigated 1,703-message session returned
1,597,893 bytes of message content and 1,822,290 bytes of text parts, totaling
3,420,183 bytes. No private message content was output or placed in the tests.

The deterministic benchmark creates 1,703 synthetic messages of 2,000 bytes
each: 3,406,000 bytes in total. Matching messages place the query at the end.
It compares the original equivalent full-message search, a cold invalidated
cache and repeated warm searches. Both matching and missing queries are
included; each variant checks its result against the original before timing.

```text
go test ./internal/ui/tui -run TestTranscriptSearch -count=1
go test ./internal/ui/tui -run ^$ -bench BenchmarkTranscriptSearchHistory1703 -benchmem
```

Regression source covers Unicode/whitespace semantics, a match beyond the
preview boundary, detached preview size, unchanged-filter menu redraw/cursor
reuse, zero allocations for warm lookup, all production message append types,
removal, folds, copied models, completed stream flush, empty/replaced history
and session load.

The initial cache-only central regression and full `go test ./...` /
`go vet ./...` runs passed before adding streaming cold matching.
On Go 1.26.2, Windows/amd64, Ryzen 7 5800X3D, GOMAXPROCS=2:

| Matching query | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| Original full scan | 14,222,760 | 15,267,977 | 5,114 |
| Cold cache / full scan | 14,449,867 | 15,326,148 | 5,215 |
| Warm cache lookup | 16.26 | 0 | 0 |

A missing query measured 13,856,158 ns / 15,259,136 B originally,
13,881,127 ns / 15,259,136 B cold and 16.89 ns / 0 B warm. The cold
matching path pays for detached previews; this change avoids repeated scans
when only cursor position changes. Warm numbers measure lookup, not scan
throughput, and the benchmark's computed MB/s for that path is not meaningful.

## Streaming cold matching

The remaining cold path still allocated normalized and lowercase copies of
every message whenever the filter changed. Short queries now compile one KMP
failure table and scan a normalized lowercase byte stream without creating
those intermediate strings. Matching stops at the first occurrence anywhere
in the complete message. A separate bounded pass builds each matching preview
with its original case and raw field bytes. No normalized history is retained.

Query normalization remains exactly `TrimSpace` followed by `ToLower`; internal
query whitespace is not collapsed. The scanner uses Go's Unicode whitespace
and simple lowercase tables. Invalid UTF-8 becomes replacement runes during
matching, as with `strings.ToLower`; preview bytes remain unchanged, as with
`Fields` / `Join`. Leading/trailing whitespace and the 512-rune preview boundary
retain their prior behavior. Cache keys, invalidation, published results and
warm lookup are unchanged.

The selector is based solely on the already normalized query. Queries longer
than 64 bytes use the previous stdlib matching and bounded-preview path.
Shorter queries also use it when the largest value in their KMP failure table
is at least 8 and at least half the query length. That maximum is accumulated
while compiling the table; there is no message pre-scan or additional history
state. The fallback deliberately retains the old whole-message allocations
for long or strongly repetitive queries, including long nonoverlapping ones.

Central tests of the isolated standard-library prototype passed. Three
300 ms benchmark repetitions on the same Windows/amd64 Ryzen 7 5800X3D host
produced these median cold-scan results; these measurements precede production
integration and include query normalization and previews but exclude cache
locking/invalidation:

| Fixture/query | Previous path, ms | Selected hybrid, ms |
| --- | ---: | ---: |
| ASCII, sparse late needle | 15.48 | 11.18 |
| ASCII, missing | 13.47 | 10.61 |
| ASCII, common alpha | 14.01 | 4.07 |
| Unicode, sparse late needle | 36.14 | 28.41 |
| Unicode, missing | 35.70 | 29.83 |
| Unicode, common alpha | 37.61 | 7.82 |

ASCII sparse late matches dropped from 15,326 KB to about 67 KB per search;
an absent short query allocated 320 bytes. Strongly overlapping queries of
16, 32, 64, 65 and 97 bytes selected the stdlib path. This avoided the initial
97-byte query's roughly 40% pure-KMP slowdown, but does not establish zero CPU
regression for every query: measured fallback variation reached about 6%
(64-byte query, 9.09 versus 9.66 ms). The selector is a measured conservative
heuristic, not a universal performance guarantee. Logs are under
`.tmp/optimization-oct8-2026/go-fts-hybrid-2.log`; the initial pure-KMP comparison
is in `go-zip-tui-experiments-fixed-3.log` in the same directory.

Production differential tests compare ordered results and exact bounded
preview bytes against an independent copy of the previous cold path. They
cover all installed Go whitespace/lowercase mappings, invalid UTF-8, query
whitespace, both selector branches, mixed case, late matches and preview
boundaries. A deterministic randomized test and fuzz target check combinations.
The production benchmarks use synthetic 1,703-message ASCII, Unicode and
invalid-UTF-8 fixtures plus short/long overlap queries; each compares the old
reference, invalidated cold cache and warm lookup. Warm throughput is omitted.

```text
go test ./internal/ui/tui -run '^TestTranscriptSearch' -count=1
go test ./internal/ui/tui -run '^$' -bench '^BenchmarkTranscriptSearchCold' -benchtime=300ms -benchmem -count=3
# Optional separate bounded fuzz run:
go test ./internal/ui/tui -run '^$' -fuzz '^FuzzTranscriptSearchColdParity$' -fuzztime=20s -parallel=1
```

Production sources/tests were formatted and frozen for central validation.
The central production checks and benchmarks below subsequently passed.

## Production validation and measurements

The complete TUI package test run passed in 6.147 seconds. A separate bounded
15-second fuzz run passed after 9,993 executions, checking ordered results and
exact preview bytes against the independent previous implementation. Completion
logs are `go-production-measurements-{0,1,2}.log` in the ignored evaluation
folder; aggregate benchmark medians are in `tui-cold-production-metrics.json`.

Three production benchmark samples of 300 ms each on Go 1.26.2, Windows/amd64,
Ryzen 7 5800X3D and GOMAXPROCS=2 produced these medians. Each fixture has 1,703
synthetic messages of approximately 2 KB. This includes real cache invalidation
and publication; it does not measure model generation or prompt processing.

| Fixture / query | Previous, ms | New cold, ms | Previous B/op | New B/op |
|---|---:|---:|---:|---:|
| ASCII, late needle | 16.579 | 12.270 | 15,326,144 | 67,312 |
| ASCII, absent | 14.068 | 11.524 | 15,259,136 | 320 |
| ASCII, common alpha | 14.304 | 4.357 | 16,432,704 | 1,173,872 |
| Unicode, late needle | 38.672 | 27.881 | 25,167,744 | 170,736 |
| Unicode, absent | 39.154 | 26.659 | 25,068,416 | 320 |
| Unicode, common alpha | 40.688 | 9.597 | 26,786,944 | 2,917,744 |
| Invalid UTF-8, late needle | 36.019 | 14.337 | 47,587,776 | 67,312 |
| Invalid UTF-8, absent | 35.993 | 14.004 | 47,520,768 | 320 |
| Invalid UTF-8, common alpha | 35.098 | 4.503 | 48,694,336 | 1,173,857 |

Warm lookup retained zero allocations (roughly 19 ns in the sampled rows).
Fallback timings for repetitive queries varied: the 16-byte case was
7.521 versus 8.006 ms, while other cases were faster or within about 2%.
The old matching algorithm remains selected for those queries; these results
do not promise every possible query is faster or that total GUI/TUI RSS falls
by the allocation difference.
