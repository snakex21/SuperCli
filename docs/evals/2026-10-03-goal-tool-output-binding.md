# Lossless on-demand tool output binding — 2026-10-03

Baseline: main 6ab0f1c / dev14. Scope: the portable session SQLite writer, not provider inference or request preparation.

## Problem and final behavior

SaveToolOutput converted every immutable output string into a new Go byte slice before the driver copied it into native SQLite storage. The retained original and saved BLOB were already necessary; the additional Go copy was not.

The writer now checks PRAGMA encoding once per Store, lazily after a successful BeginTx and through that transaction. For UTF-8 it binds the string and uses CAST(? AS BLOB). UTF-16, unknown encodings and a failed encoding query permanently retain the original byte binding. The first failed validation/BeginTx does not consume the lookup. The query failure never replaces the original save error.

No schema migration, output limits, handle rules, model/UI preview, provider messages, native reasoning, cache entries or persistent data format changed. The Store retains 16 additional constant bytes on windows/amd64: sync.Once plus the encoding decision (48 → 64 bytes). Opening a store and a short output that is not persisted perform no encoding query.

An ungated CAST prototype was rejected: on a UTF-16 database, the original three-byte a-NUL-b value became a six-byte BLOB while its byte metadata remained three. Durable tests exercise UTF-8, UTF-16LE and UTF-16BE, empty text, embedded NUL, Unicode and all 256 byte values. The fallback preserves raw BLOB bytes; this change does not claim new general UTF-16 read support.

## Evidence and matched measurement

A bounded read-only aggregate from the same six completed sessions used in earlier output audits found 238 currently retained outputs totaling 8,924,653 bytes. Their size percentiles were 12,311 / 34,404 / 168,382 bytes (p50 / p90 / p99), with a maximum of 4,382,834 bytes. These are retained rows, not all historical saves or a measurement of wasted turns. No prompts, session IDs or output content are published.

Windows/amd64, Ryzen 7 5800X3D, Go portable caches, CGO_ENABLED=0 and GOMAXPROCS=2. Three alternating matched pairs ran each public benchmark for 16 operations, serially without other CPU benchmarks or model calls. Warm persistence was primed outside the timed region; first-save cases used a new store/session with setup excluded. The measured public ModelContentContext pipeline includes preview creation, OutputStore retention and real SQLite persistence. Numbers below are medians of the three runs.

| Public pipeline / output bytes | Baseline Go B/op | Final Go B/op | Baseline ms/op | Final ms/op |
|---|---:|---:|---:|---:|
| FirstSave / 12311 | 15,949 | 3,301 | 0.727 | 0.698 |
| FirstSave / 1048576 | 1,076,325 | 28,595 | 5.204 | 4.109 |
| ModelContent / 512 | 282 | 282 | 0.103 | 0.102 |
| ModelContent / 12311 | 28,641 | 14,153 | 0.837 | 0.787 |
| ModelContent / 34404 | 56,251 | 15,611 | 0.962 | 0.887 |
| ModelContent / 131072 | 155,242 | 24,156 | 1.774 | 1.692 |
| ModelContent / 1048576 | 1,208,486 | 158,785 | 10.710 | 10.221 |

Warm SaveToolOutput at the observed 4,382,834-byte maximum fell from 5,071,177 to 680,114 Go B/op. A first save adds about 19 small query allocations; at 12,311 bytes it changed 57 → 76 allocs/op, while still reducing allocated bytes. Subsequent calls reuse the decision without a SQL encoding query. Store-only opening kept 5,395 allocs/op; its +16 B/op matches the constant metadata increase. The unpersisted 512-byte ModelContent control remained 282 B/op and 6 allocs/op.

The reproducible benefit is the removed transient Go copy. SQLite latency was noisy: for example, final first-save 12,311-byte runs ranged 0.669–1.905 ms. No request token, native decode/prefill TPS, end-to-end turn count, process RSS or general latency improvement is claimed. Native SQLite still copies the bound data; complete evidence still resides in its original output and durable BLOB. The largest warm bind benchmark crosses the existing 64 MiB retention limit and includes normal eviction in both arms.

## Verification

The ignored baseline and final-prototype fixtures passed focused storage controls. The final tracked checks are recorded after port below. Controls cover one-time query failure/unknown encoding, UTF-16 raw BLOB fallback, cancellation and validation priority, immutable handles, metadata byte lengths, restart/reference resolution, concurrent outputs, existing 16 MiB / 64 MiB bounds, and fresh OutputStore/read_output retrieval for successful and failed tool results. No live model was needed for unchanged persistence bytes.

Portable private receipts and overlay runners: .tmp/goal-runtime-round12-2026-10-03. They include all matched benchmark runs, redacted size aggregates, rejected UTF-16 proof and baseline/final hashes.

Final tracked checks (PASS): `go test ./internal/storage/session -count=1` (4.511 s); focused agent cancellation/worker/resume/output ownership persistence regressions (2.398 s); `go vet ./internal/storage/session ./internal/agent`. Gofmt touched only the two owned source files and new binding test. The final lazy-binding port uses the same measured operations as the overlay; no additional benchmark or model request followed these checks.
