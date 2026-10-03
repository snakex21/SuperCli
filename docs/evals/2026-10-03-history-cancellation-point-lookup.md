# Cancellable history reads and exact rewind selection

Date: 2026-10-03
Baseline: a60c4abddf68122e43aa568eac8762766fd6d1fe (dev21)

## Reproduced problems

`Store.ReadMessages(ctx, sessionID)` accepted a cancellation context but used `db.Query`. The isolated baseline returned all eight messages after cancellation and kept waiting when the only database connection was occupied. The candidate uses `QueryContext`. Pre-canceled reads now return `context.Canceled`, including when the connection pool is occupied; a later healthy read still succeeds.

GUI `handleSessionRewind` loaded every message only to validate the selected sequence. It now uses `ReadMessageAt` with the existing indexed `(session_id, seq)` key. The exact selected row is decoded and checked for user role and legacy compaction provenance as before. Missing/non-user/malformed selections remain HTTP 400, before attachments, checkpoints, or history can be changed. No new index, cache, timer, instruction, inference, or dependency is added.

## Isolated ABBA measurements

Portable local Go 1.26.2, Windows amd64, Ryzen 7 5800X3D, GOMAXPROCS=2. Baseline/candidate/candidate/baseline overlays froze the two source files and exercised the actual store and HTTP handler. Four runs used `-benchtime=150ms -benchmem`. Fixtures and engine startup are excluded from timers. All fixtures used local temporary databases and echo providers; no live inference, user configuration, or application process was touched.

| Case | Baseline | Candidate |
| --- | --- | --- |
| Pre-canceled read, 8 KiB history | 42.93–43.91 µs; 12,792 B; 88 allocations | 81.07–93.68 ns; 16 B; 1 allocation |
| Pre-canceled read, 4 MiB history | 3.72–4.09 ms; ~4.25 MB; 1,128 allocations | 76.67–81.94 ns; 16 B; 1 allocation |
| Rewind selector, 7×1 KiB unrelated payloads | 222.87–235.15 µs; ~22,011 B; 177 allocations | 201.39–204.03 µs; 11,544 B; 124 allocations |
| Rewind selector, 7×512 KiB unrelated payloads | 3.10–3.20 ms; ~3.71 MB; 740–741 allocations | 221.59–237.20 µs; 11,646–11,703 B; 126 allocations |

The HTTP benchmark selects a non-user row and verifies the rejection path. It isolates selection before mutation; it does **not** measure an entire successful rewind or claim total GUI latency. The exact query also serves successful rewind, whose existing tests verify file restoration, attachment recovery, metadata, and checkpoint behavior.

Healthy full-read allocation counts remain unchanged (12,793 B / 88 allocations at 8 KiB; ~4.25 MB / 1,128 at 4 MiB). Healthy timings ranged 38.33–43.30 µs baseline versus 45.21–50.20 µs candidate at 8 KiB, and 3.90–4.19 ms versus 3.47–3.61 ms at 4 MiB. These short runs do not establish an ordinary read speed gain; the justified benefit is honoring cancellation and avoiding unrelated rows for exact selection.

Allocation reduction is conditional per operation, not a measured reduction in application RSS or WebView2 memory. These changes do not alter model token counts, generation rate, or prompts. Frequency in real sessions is not established.

## Verification

- The frozen baseline fails the two cancellation regressions with the expected rows/waiting assertions, and passes healthy parity/rewind controls.
- The isolated candidate passes cancellation, full-row parity, existing rewind, and internal summary rejection controls.
- Production storage tests cover canceled reads, occupied connection cancellation, healthy recovery, all encoded fields, embedded NUL/Unicode, nullable metadata, exact session isolation, sparse maximum integer sequence, missing sequence/session, and closed-store errors.
- Production GUI tests preserve successful rewind with attachments/files and reject missing/non-user/malformed selection without changing history. The webgui package passed the initial focused run; a test fixture initially omitted required `created_at`, which was corrected before the storage package passed.

Private proof artifacts: `.tmp/goal-history-lookup-2026-10-03`. Full integration validation is recorded separately for this tranche.

## Integrated validation

The final combined source passed the full Go suite (71 tested packages and 28 packages without tests), `go vet ./...`, formatting, and diff checks. Source hashes (1,866 files under cmd/internal/test plus module manifests) stayed identical through checks. No frontend file changed; the prior dev21 run of 194 Node UI tests was retained rather than repeated. Live Qwen/Zen/AnyRouter calls were not used for these DB/callback changes.

All ten CGO-disabled CLI/GUI builds passed for Windows amd64, Linux amd64/arm64, and macOS amd64/arm64. Build source snapshots matched the tested source. Both local Windows EXEs were installed as `1.0.4-dev.22`, with exact old/new hashes and portable backups verified. CLI version/help and GUI target/linked-version checks passed. No user app was closed, activated, or restarted. Cross-platform execution and native GUI rendering were not measured. Artifact/check manifests are under `.tmp/goal-integrated-dev22-2026-10-03`.
