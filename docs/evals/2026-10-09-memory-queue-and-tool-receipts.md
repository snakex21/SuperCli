# Completed queue and successful tool-result retention — 2026-10-09

This batch removes redundant copies without adding model calls, prompt instructions, dependencies, or rewriting existing conversation history. It preserves the special OpenCode Zen route and portable application data.

## Embedding queue

After draining the embedding queue, its reusable backing array still held completed Entry strings and tag slices. Clear those slots before resetting the length; retain capacity for the next batch.

The permanent regression uses real Store.Put, SQLite storage and weak references to the original source buffers. Before the fix it fails with the embedder enabled and disabled. After the fix both paths release source buffers; persisted notes and expected vectors remain. Focused tests passed three repetitions.

A matched Go heap experiment queued 128 distinct 16 KiB notes, flushed once, and kept the store open. It warmed the store beforehand, used explicit GC, and retained the same 129 persisted notes/vectors/embedding calls (including warm-up) in both variants. Across three runs:

| Measurement | Before | After |
|---|---:|---:|
| Original source buffers still reachable | 128 | 0 |
| Original source bytes retained | 2,097,152 | 0 |
| Median HeapAlloc increase | 2,142,608 B | 42,480 B |

The median heap difference is 2,100,128 B. This is controlled Go retained heap, not process RSS or a promised reduction for ordinary smaller notes. The 256-entry drain benchmark median changed from 6,435 to 7,672 ns; clearing adds approximately 1.237 microseconds per batch. Both variants allocate 49,152 B in one allocation. This is a memory tradeoff, not a faster drain claim.

Source: internal/storage/memory/feature_hybrid.go. Permanent test: internal/storage/memory/embedding_queue_retention_test.go. Portable experiment, overlay and logs: .tmp/optimization-oct8-2026/embed-queue-retention and go-embed-queue-heap-oct9-{0,1}.log. No model inference is involved.

## Successful remember results

An accepted note already appears in the paired tool-call arguments. The successful result previously repeated the entire note. ModelText now contains only the existing memory ID, normalized type and actual destination. Full Result.Text and stored content are unchanged. Errors, unavailable keepers and routing retain their previous behavior. The inline receipt creates no read_output handle or output persistence I/O.

The sanitized audit sampled 30 completed sessions and 1,831 stored tool calls: 10 remember calls, of which eight successes repeated an identical accepted note. Those eight full results occupied 7,349 B, versus 392 B for their receipts: 6,957 B of duplicate text. Two error results were excluded. This is the result payload difference, not a measured token count or a claimed reduction in agent turns.

A separate generated transport experiment used eight successful calls per request and actual Chat Completions, Responses and Anthropic request assemblers. Full arguments and all other decoded request fields remained identical; only result echoes changed. All three formats produced the same byte reduction:

| Accepted note size | Bytes removed from each request |
|---|---:|
| 872 B | 7,024 B |
| 1,362 B | 10,944 B |
| 4,096 B | 32,816 B |

These are serialized wire bytes, including escaping. Production tests separately verify real persisted Unicode/CRLF/quoted content, the 4,096-byte limit, exact full Result.Text, scope fallback, failure causes and zero extra persistence. Existing saved results are not migrated. New model-facing history carries the receipt, with the full note in paired assistant arguments and durable memory; lossy compaction and corrupt legacy arguments are not given a new exact-archive guarantee.

Source: internal/tools/memorytools/memory_tools.go. Permanent tests: internal/tools/memorytools/remember_projection_test.go. Portable audit: .tmp/optimization-oct8-2026/remember-success-projection-audit.json. Valid-size wire fixture: remember-wire/experiment_test.go and go-remember-wire-valid-oct9-0.log in the same portable directory. An earlier exploratory wire run included a 4,097-byte fixture and is excluded from the reported comparison.

## GUI delegation argument copies

After successful materialization of a task/send_message brief and activity container, release both row._toolArgs and lexical args. Full _taskPrompt/brief/report, worker controls, continuation backlink and disclosure state remain. Keep raw input if materialization fails. Clearing only the row property does not free the string retained by the event-handler closure.

The frozen V8 workload contained 20 alternating task/send_message rows with 1,000 generated lines per brief, 1,028,380 B raw JSON and 967,800 B canonical briefs. Canonical briefs, DOM and reports remained alive. Root independently reproduced the comparison:

| Measurement | Before | After |
|---|---:|---:|
| heapUsed | 9,491,936 B | 8,463,824 B |
| DOM nodes | 339 | 339 |
| Canonical brief records | 20 | 20 |

The heap difference is 1,028,112 B. Brief/report hashes are identical. Removing only the property produced no release. A positive control with another live raw-JSON owner also produced no release. WeakRef tests fail for baseline/property-only and pass for the fix, while externally owned raw input and ordinary folded tool input remain alive. These are generated V8 results, not total WebView2 RSS, user-input latency or FPS claims.

Source: internal/webgui/assets/js/04-transcript.js. Four added regressions and one adjusted expectation reuse test/ui/history-tool-lazy.test.cjs. Detailed snapshots, exact commands, controls and hashes: .tmp/audit-runtime/task-input-2026-10-09/RECEIPT.md. Root reproduction: .tmp/optimization-oct8-2026/root-gui-heap-oct9-i.json.

## Final validation and local installation

Validated source SHA-256: 80f7e3a68f5659a387509bc5e383e2c16de265850abbec764aefe5862d5a277e.

- All 99 Go packages covered: 71 test-bearing packages passed; 28 have no test files. The 176 checkpoint cases were run in three complete, disjoint chunks.
- Go vet passed; all 222 UI tests passed; git diff --check passed.
- Ten binaries built and hash-verified: TUI + GUI for Windows amd64, Linux amd64/arm64 and macOS amd64/arm64. Cross-built artifacts are not runtime-tested on other operating systems.
- Both local Windows EXEs replaced successfully; both --help smoke tests passed. No running application was forcibly closed or restarted.
- supercli.exe: 26,914,304 B; SHA-256 b2b5a616dfe82a4ae2c04bce2d57c8ea9bc83429e4fda701918e4ca44686f6b1.
- supercli-web.exe: 23,835,648 B; SHA-256 726046d37206293ac4f1912afa8493466b40366bc9eeb9ac0faf4c7a30ee9dbf.

Validation/install/smoke receipts: .tmp/optimization-oct8-2026/validation-oct9-i.json, validation-oct9-i-pending.json, final-oct9-i.json and smoke-oct9-i.json. Rollback copies are preserved in binary-backups/1791549462119. Tests and builds used portable temporary/cache paths; the existing module cache was read-only. No new model inference, TPS, end-to-end prompt-processing or agent-turn measurements were made. No commit, push or release was performed by this batch.
