# Retained outputs across turns and restart

## Defect and regression

Every OutputStore generated out_000001 as its first reference. GUI turns recreate their registries, so a historical reference could resolve to unrelated new output. A regression reproduced the wrong bytes before the fix (.tmp/output-resume-before.json).

Results now receive 128-bit random handles. Ordinary small results retain their existing inline behavior. Large previews and omitted diagnostics retain the original bytes in the existing portable sessions.db via an optional writer interface. The schema and instructions of read_output are unchanged.

## Storage and execution behavior

- The in-memory LRU remains limited to 32 results / 16 MiB. Persistent payloads are limited to 256 results / 64 MiB globally, with a 16 MiB per-result cap; oldest entries expire. SQLite file/WAL size also includes metadata, free pages and transaction overhead, so 64 MiB is a payload bound, not a total file-size promise.
- Each retained result is saved once before advertising persistence. Ordinary inline results make no additional database call. A failed or canceled save keeps the in-memory result and labels its lifetime accordingly, without retrying the original tool.
- Opening a loop or resuming a session does not preload output blobs. A read_output memory miss loads the referenced result; subsequent reads use the bounded memory cache. Chunk and search limits remain unchanged.
- Opaque references can be carried in resumed/forked histories and worker reports; lookup is by handle, not the currently selected session. There is no output-list tool. The producing session owns the cache row; deleting that session removes its rows, including references used elsewhere.
- Workers inherit only output persistence, not the parent's conversation writer. Their transcripts remain separate.
- The provider-independent loop wires persistence for GUI/TUI and workers. No helper model call, local/cloud branch or added system instruction. OpenCode Zen transport and request headers are untouched.

This applies to newly retained outputs. Old process-only results cannot be reconstructed from their truncated previews. Expired references produce an error rather than resolving to another result. Full text still enters the prompt only through bounded chunks/excerpts requested by the model.

## Verification

- New-store collision regression: stale reference cannot return another result.
- Portable DB close/reopen, immutable references, deletion, canceled writes, item/byte retention limits, parallel writes.
- Small-result zero I/O, memory hits avoid repeat disk reads, failed save remains usable with truthful lifetime metadata.
- Actual GUI loop rebuilt after engine restart: both native and sentinel reads retrieve the original hidden diagnostic despite a different intervening result. Original tool executes once; the continuation uses a read plus final response (two model calls).
- Worker result survives in persistence and is readable by the parent without copying the worker conversation into its session.
- Existing UTF-8 paging, output bounds, structured file previews and command-failure tests remain passing; fixtures now follow returned handles.
- All 64 tested packages pass go test ./...; go vet ./... and git diff --check pass.

## Cost measurement

BenchmarkSaveToolOutput: 128 KiB result saved to local SQLite, including retention enforcement. Three runs of ten iterations on Windows / Ryzen 7 5800X3D: 1.498, 1.476 and 1.501 ms per save. This is a storage microbenchmark, not an end-to-end model speedup. No live model evaluation was run for this change.

Raw records: .tmp/output-resume-benchmark.json and .tmp/output-resume-checks-final.json.
