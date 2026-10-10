# Checkpoint and worker optimization, 2026-10-09

This pass resumes the optimization goal. Earlier October8/9 validation and installed binaries are separate from these new source changes. The integrated managed checkpoint retention is now validated and installed in both local Windows binaries. It targets reclaimable history, with an explicit protected-floor exception; it is not a strict cap on every application byte. Existing running applications were not restarted.

## Measured allocation reduction

Workload:64 distinct new1KiB Git blobs, with an empty object directory each iteration. Fixture preparation and cleanup are outside the timer. Windows amd64, Ryzen7 5800X3D, GOMAXPROCS2, five iterations per sample and three samples.

| Measurement | Previous | Capture-local encoder |
| --- | ---: | ---: |
| Median time |57.475ms |50.820ms |
| Allocated bytes per operation |52,585,072 |1,207,267 |
| Allocations per operation |4,493 |3,092 |

The encoder is lazy and local to one capture, with one resettable compressor and32KiB copy buffer. There is no global pool or retained compressor between model requests. Existing-object deduplication still happens before compression. Compression level and raw Git identity remain unchanged.

The allocation reduction is97.70%; median workload time improves11.58%. These are allocation/workload results, not whole-process RSS or provider token-speed measurements.

Canonical blob OID/read-back, growth, same-size content replacement with restored mtime, temporary-file cleanup and reset-after-error regressions passed three times. Logs are in the portable optimization fixture directory.

## Delegation and identity

A checkpoint mutation covers before capture and the actual tool Fn through return. Accepted mutating worker invocations borrow the original turn before their context detaches. Retained worker continuations bind the new invocation's turn. Read-only advisors do not delay foreground completion.

Only unfinished accepted work creates one event-driven completion waiter. An ordinary completed turn remains inline. Waiters retain checkpoint state and a completion sink, not a closed SSE writer or parent request context. A unique native lifetime lease protects pending workers even before their first file tool. The short store gate is not held during model calls or tool execution.

Managers reload fresh metadata inside a shared portable store transaction. Recorded cleanup failures retain the owner; a later explicit restore/clear can retry only cleanup, without recapturing later manual edits. Interrupted unknown snapshots remain protected and are reported as requiring recovery. No PID/age guess permits deleting them.

GUI checkpoint user sequence comes from the exact committed insert receipt, removing a latest-user SQL query and preventing a later worker notice from changing ownership. Deferred file-change telemetry targets the exact AUTOINCREMENT summary row, because history rewind can reuse message sequence numbers. It never writes to an already closed SSE response. A late SQL failure does not lose the durable checkpoint; durable telemetry retry is a separate remaining task.

## Storage boundary

New completed records will retain minimal before/after trees containing only their changed files, with the original blob OID and mode. Live full snapshots remain pinned until metadata and record refs are committed. This change does not itself migrate old records or reclaim unreachable objects.

The 1GiB managed history collector, accounting and interruption recovery have now passed integrated validation. Expiry runs at completed mutating turns, after accepted workers and active pin cleanup; it does not add a model request or an idle timer. Read-only/chat turns initialize no ledger. Protected live/interrupted roots and unknown stores remain protected, using the explicit floor policy below. The resumed pass losslessly narrowed four inert badcheckpoints archives (46 records, 92 verified before/after sides). Fresh all-ref reachability proof then removed only regular unreferenced loose objects, reclaiming 12,571,873,351 file bytes. Record order, changed paths, original blob OIDs/modes, missing paths, arbitrary metadata and legacy RawBytes=false were preserved. Windows read-only Git objects and interrupted/resumed reclamation are covered by 411 synthetic checks. Audit backups and apply/reclaim receipts remain portable. Live checkpoint repositories and project files were not reclaimed by this archive operation.

A subsequent complete file-length census measured 28,609,948,944 bytes (26.645GiB) in the application folder, including 12,810,924,620 bytes of development/test/cache data in .tmp. Compared with the original 111,594,039,937-byte baseline, the folder has shrunk by approximately 77.3%. This is disk usage, not process RAM or NTFS cluster allocation. Separate live application activity can add new data; these are dated measurements, not a strict installed-runtime quota claim.

LM Studio was unavailable at both separately started resumed test attempts (07:44 and 09:22 UTC, ECONNREFUSED; zero model calls). This pass's worker/download regression tests use synthetic providers; no new Qwen/Zen/Kilo inference result is claimed.

## Validated integration

The new accounting hooks charge compressed bytes already passing through the writer, existing path metadata and exact encoded metadata/ref bounds. They do not reread blobs or keep a graph/cache between requests. Post-turn retention runs only after accepted work finishes and active lease/ref cleanup succeeds. Read-only/chat turns initialize no quota ledger. The complete checkpoint package and all other test-bearing packages passed for the installed source hash.

The installed managed policy targets 1GiB of reclaimable history. A full census may establish a protected floor (live/interrupted roots, legacy indexes, unknown stores and other mandatory data). When that floor alone exceeds the base limit, the effective bound is base plus that proven floor. A bounded scalar receipt avoids repeating a full census on every small turn; invalidation or pressure requires fresh evidence. This is not a strict 1GiB cap on all application data or on protected recovery data.

Current source integration also adds immutable AUTOINCREMENT user-message identity to checkpoint ownership, gate-coordinated exact transcript rewinds/deletes and invocation-scoped persistence writers. Legacy records without immutable ownership are excluded from automatic chat rewind; explicit checkpoint-ID recovery remains available. These changes passed full-source validation and are installed in both local EXEs.

## Same-input scoped record fastpath

Workload: the same already-scoped before/after commits containing 64 changed 1KiB files. Both paths retain exactly the same canonical commit OIDs. Preparation, common caller gate and accounting are outside this step timer; the gate and Turn mutex are held for both implementations. Windows amd64, Ryzen 7 5800X3D, GOMAXPROCS=2, 100 iterations per sample, three samples.

| Measurement | Previous narrow step | Proven scoped reuse |
| --- | ---: | ---: |
| Median time | 76.449ms | 4.144µs |
| Allocated bytes/op (median) | 209,866 | 3,592 |
| Allocations/op | 1,875 | 6 |
| Git calls in step | 2 | 0 |

Reuse requires every scope root to occur in the actual changed-file set. Directory roots with unchanged backup files and whole-workspace captures keep the existing narrowing path. Raw bytes, modes, missing paths and exact canonical trees are regression-tested. This is a saved processing step, not total capture or model-task time. Existing file/directory Undo support remains a separate candidate, not a claimed fix.

## Completion metadata and accounting cost

Manager-level benchmark: an identical 1,500-record, 1,425,395-byte turns.json document, clean verified accounting and actual shared gate; 100 iterations, three samples. Initial fixture/census is outside timing.

| Measurement | Previous double decode | Single decode |
| --- | ---: | ---: |
| Median time | 22.499ms | 11.082ms |
| Allocated bytes/op (median) | 10,409,323 | 5,210,555 |
| Allocations/op | 30,183 | 15,129 |

The mandatory initial gated reload remains, preserving other managers' changes and successful journal recovery. A second reload occurs only if collection ran, including errors after partial durable expiry. Failed journal recovery also refreshes metadata while the gate is held. Regressions cover another manager's append, actual expiry and interruption after metadata publication but before unavailable Git ref cleanup.

Counter-only benchmarks (actual gate plus bounded ledger): strict clean completion median 0.201ms / 6,776 B, protected-floor clean completion 0.195ms / 7,272 B, durable Begin+Finish receipt 4.268ms / about 35,648 B. The receipt performs two durable writes; the clean counter path does zero census/Git/writes. These figures exclude Manager metadata decoding. Initial benchmark samples with a stopped timer were rejected; only corrected samples explicitly calling StartTimer are used here. Accounting has real write overhead and is not described as free.

Fresh private Git repositories explicitly select the files ref backend. Existing reftable stores keep accounting dirty through ref mutation, because updates and deletions can append table data beyond a 41-byte loose-ref bound. A regression with the real backend verifies fresh census catches this growth. This adds one exact repository lookup to a new accounting transaction, without graph reads.

## Recoverable session media deletion

Before attachment-directory rename, a bounded portable intent is synced. A marker with operation ID, original hash and quarantine name commits in the same SQLite deletion transaction. After interruption, an absent marker restores captured media only into an absent original; a matching marker cleans only the captured unique tree. New media for a reused SID is preserved. A failure/partial cleanup retains marker and intent. If the captured tree is already absent after marker retirement, recovery retires the intent and never touches a new original.

GUI and TUI startup each probe one reserved namespace when opening the Store. Only a present namespace acquires StoreGate and performs bounded recovery (at most 1,024 entries, at most 1KiB per intent); no entire-history/media scan or per-turn probe is added. The SQL table is created lazily for explicit deletion. Tests cover rollback/commit reopen, moved portable root, occupied original and exclusive native rename, partial remove, SQL marker-retirement failure, malformed/missing schema and bounds. Windows/Linux/Darwin use no-replace rename; unsupported systems fail safely.

## Final validation and installation

Source hash: 0cbfbfc8f3653fb3c880f284c227eef748eba5466b4df12dc08bff7fdb87cb18.

All 99 Go packages were covered in six completed test groups: 71 test-bearing packages passed, 28 had no tests. go vet passed. UI: 218 passed, zero failed. The initial monolithic test invocation exceeded the tool/kernel deadline and supplied no completion proof; it is not counted. Each completed group was source-hash checked, and exact package-union coverage was verified before sealing.

CLI and GUI builds succeeded for Windows amd64, Linux amd64/arm64 and Darwin amd64/arm64 (10 outputs). Both local Windows EXEs were atomically staged, hashed, installed with portable recoverable backups, and passed --help smoke checks. Cross-compilation is not runtime verification on Linux/macOS. No user application was closed/restarted, no new inference result is claimed, and special Zen route files stayed unchanged.

Portable receipts/logs: .tmp/optimization-oct8-2026/validation-oct9-g.json, validation-oct9-g-pending.json (target hashes), final-oct9-g.json, help-smoke-oct9-g.json; corrected checkpoint benchmark logs and session-media-delete-recovery.md in the same directory. The optimization goal remains active; this report describes a completed validated batch, not exhaustion of every possible optimization.
