# Cheaper concurrent restoration of saved output — 2026-09-24

## Problem and scope

The session log contains 162 read_output results. Among 129 parsed read_output
calls in the inspected recent assistant records, four batches ask for the same
handle more than once. This establishes that parallel reads of one result occur;
it does not prove those historical calls all missed the memory cache.
At inspection, the persisted output table held one 9,172-byte row.

On an OutputStore cache miss, simultaneous readers previously fetched and copied
the same entire result independently before discovering that another reader had
already cached it. The SQLite reader also scanned into []byte and then converted
to string, adding an avoidable full-size copy.

## Implementation

- Scan SQLite content directly into the final string, preserving all original
  bytes, including NUL and invalid UTF-8. Stored rows and the database schema do
  not change.
- Within one OutputStore, one in-flight read owns each immutable handle. Other
  callers wait for completion without polling. Different handles remain parallel.
- A waiting caller can cancel independently. If the owner is canceled, a still
  active waiter retries using its own context. Errors are not cached.
- Completion removes the in-flight entry. Backend panic still propagates to its
  owner but releases waiting callers and permits later retries.
- Memory hits keep the existing LRU path. The 32-item / 16 MiB active-cache limits
  and existing persistence limits are unchanged.

This does not combine requests across separate OutputStore instances, add a
background fetch, or prefetch output when a conversation opens. Tool definitions,
returned previews/chunks, system instructions and provider requests are unchanged.
The shared implementation serves CLI/TUI/GUI with local and cloud models.
The special Zen transport/protocol was not modified.

## Measurements

Windows / Ryzen 7 5800X3D, real portable SQLite database with the current session
store configuration. Three 200 ms samples per initial/final case; an additional
three 300 ms samples reverse before/after order. Data is synthetic and setup is
outside the timed loop.

"Cold" means a new SuperCLI OutputStore, not a flushed OS filesystem cache.
The eight-reader cases release eight goroutines together and read bounded
128-byte chunks from the same handle. Times include the complete tool batch;
database connection/cache behavior is included. Database-only cases isolate the
copy removal from the in-flight coordination.

| Case | Before median | Final median | Before DB reads | Final DB reads |
|---|---:|---:|---:|---:|
| 9 KiB, database read | 28.35 us | 26.43 us | 1 | 1 |
| 9 KiB, one cold reader | 44.47 us | 44.11 us | 1 | 1 |
| 9 KiB, eight cold readers | 2.110 ms | 0.063 ms | 7.93 | 1 |
| 9 KiB, eight memory hits | 18.04 us | 18.60 us | 0 | 0 |
| 1 MiB, database read | 1.114 ms | 0.925 ms | 1 | 1 |
| 1 MiB, one cold reader | 1.593 ms | 0.893 ms | 1 | 1 |
| 1 MiB, eight cold readers | 8.891 ms | 0.495 ms | 8 | 1 |
| 1 MiB, eight memory hits | 18.28 us | 17.97 us | 0 | 0 |

For the 1 MiB / eight-reader batch, allocation bytes fall from 25,298,846 to
2,110,290 (about 92%). The database-only read falls from 3,146,432 to 2,097,847
bytes (about one full MiB saved). These are allocation bytes per operation,
not resident or peak RAM.

The first two comparison pairs showed 8.891 -> 0.519 ms and 9.057 -> 0.508 ms
for the 1 MiB concurrent case. The final cleanup version measured 0.495 ms.
A lone 9 KiB cold read stays approximately equal: coordination adds a few small
objects, while removing the extra text copy reduces total bytes.
Memory-hit timings vary slightly in either direction and keep the same allocation
count. There is no claim that ordinary chat or every tool call speeds up by this
factor, or that provider-token usage falls.

## Validation

Passed:
- Deterministic channel-based tests proving one persistence read for eight waiting
  callers, parallelism across different handles, independent cancellation, retry
  after owner cancellation, failure/size-limit recovery, and panic cleanup.
  These tests do not use polling or sleep-based synchronization.
- Byte-exact SQLite round trips for empty text, UTF-8, legacy bytes, NUL and
  invalid UTF-8; canceled-read behavior.
- Existing restart/history-reference, persistence, eviction and worker-output
  tests; go test ./... and go vet ./....
- CLI and GUI builds.

No live-model inference is necessary to measure this internal read optimization:
the tool format and supplied evidence do not change.

## Reproduce

go test ./internal/storage/session -run ^$ -bench ^BenchmarkToolOutputRestore$
-benchmem -benchtime=200ms -count=3

The fixture is written below the test temporary directory. In this workspace,
TEMP/TMP and Go build caches are explicitly set under the application .tmp folder.
Raw output, medians, before-source overlay, test results and install manifest:
.tmp/output-restore.
