# Memory mirror position projection — 2026-10-09

After each durable memory mirror write, verification needs entry IDs and line offsets. The old path read complete contents into another Entry slice even though only ID, LineStart and LineEnd were consumed. mdReadPositions scans the same header expression and line boundaries without retaining body text. Full mdRead and imports/content consumers are unchanged. Mirror generation checks, transaction/outbox ordering, atomic writes, FTS and error handling remain unchanged.

## Real write benchmark

The identical benchmark calls Store.Put and includes its durable mirror write/verification. Synthetic seeding is outside the timed region. Results are medians of three 150 ms runs, Windows amd64, GOMAXPROCS=2.

| Fixture | Before time | After time | Before allocated bytes | After allocated bytes | Before allocations | After allocations |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 1000 short entries | 11.477 ms | 11.080 ms | 2,559,301 | 1,810,871 | 27,151 | 23,146 |
| 200 entries, 4 KB each | 12.900 ms | 11.962 ms | 10,182,756 | 6,860,124 | 27,004 | 4,992 |
| 200 entries, 16 KiB each | 38.943 ms | 33.386 ms | 47,786,950 | 27,031,067 | 93,148 | 7,915 |

Allocated bytes are per operation, not retained heap, GUI process RSS or a provider/model speed claim. The 200-entry fixture follows the task-log capacity but uses the fact scope to isolate the existing general mirror path; it does not claim every foreground turn writes the complete task log.

## Verification

The full memory package passes. New parity cases compare projected positions to the full parser for empty/preamble-only files, CRLF, Unicode text, whitespace, absent final newline, malformed and embedded headers, repeated IDs, large multiline contents, missing paths, directories and scanner failures. Existing mirror/outbox tests also cover shifted positions, unchanged positions, stale renderers, write failures and crash recovery.

The parser continues to surface existing open/scanner error text and returns no partial positions after a read failure. It uses the existing Scanner limits and header expression. Persisted memory content is not shortened, removed or summarized by this optimization.
