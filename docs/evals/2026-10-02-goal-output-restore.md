# Saved tool-output restore: driver string path

Measured on Windows amd64, Ryzen 7 5800X3D, Go 1.26.2, GOMAXPROCS=2, modernc.org/sqlite v1.52.0. Four sequential runs in A-B-B-A order, 20 iterations per case. The baseline is main 3eae0a5; candidate differs only in the ReadToolOutput SELECT. Full raw output: local .tmp/goal-output-cast-2026-10-02/bench.json (not shipped).

## Change

Read immutable cached output as `CAST(content AS TEXT)` at the SQLite driver boundary. The modernc driver returns an owned string for TEXT; the previous BLOB path returned an owned byte slice that database/sql then copied into the destination string. Storage remains a BLOB, immutable handles and byte limits remain unchanged. No schema migration, new dependency, provider change, prompt instruction or background process.

## Measurements

| Case | Baseline | Candidate | Allocated bytes/op |
| --- | ---: | ---: | ---: |
| 1 MiB database read | 567.95 us | 433.66 us | 2,097,852 → about 1,049,408 |
| 1 MiB cold read_output | 523.28 us | 424.71 us | about 2,100,400 → 1,051,721 |
| 9 KiB database read | 33.37 us | 30.92 us | 19,637 → 10,173 |

Values are arithmetic means of the two run results per variant, not a large-sample latency claim. Warm read_output still performs zero database reads; eight concurrent cold readers still perform one database read per handle. Scheduling noise dominates small/parallel cases, so no speed claim is made for them. The retained final string stays the same size; the saving is one transient Go copy, not a guaranteed reduction in total process working set. SQLite native allocations are not included in B/op.

## Correctness

Existing restart, resumed-session/worker opaque-handle resolution, owner deletion, bounded eviction and canceled read/write tests are retained. The read parity fixture additionally exercises every byte value repeatedly, UTF-8, emoji, embedded NUL, malformed/legacy byte sequences and empty content, and confirms the persisted value remains a BLOB. No cache entry is dropped early and no result is shortened.
