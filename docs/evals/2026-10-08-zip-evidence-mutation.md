# ZIP evidence and mutation semantics — 2026-10-08

## Observed case

Session 55a7401bf03e6792 looked for a manifest in part01 (seq 825), listed two explicit parts (829), then read it from part02 and compared a local manifest (837). These are different useful operations, not 326 identical/redundant ZIP calls. The previous ZIP tool only supported list/extract. A bounded text read over known parts removes the need to guess a part or write extracted files merely to inspect JSON. The separate comparison still remains real work.

## Implemented

- Existing dormant read_zip now supports action=read: one path or 1–16 explicit paths, required glob, maximum 16 matches, 32 KiB text by default / 64 KiB maximum, full UTF-8 contents and exact archive/entry identity. No binary/base64 or silent truncation; budgets checked before decompression. Existing 256 MiB aggregate archive-on-disk and 10,000 aggregate-entry limits remain.
- This schema is discoverable only when needed; no system/coordinator prompt instructions or additional always-on tool were added.
- extract is a mutation; list/read are observations. Verified non-inert extraction expires previously failed command fingerprints locally and for a worker sharing the same workspace. It does not mark a failed check as passed. Zero matches are inert and do not forgive failed checks.
- Reads create no checkpoint. Explicit target_dir extraction captures that directory, preserving existing bytes and absence of newly extracted files, without snapshotting unrelated large archives. An unspecified destination retains conservative full-workspace fallback because custom ExtractRoot instances exist. TUI/GUI registrations carry the checkpoint wrapper; dormant behavior unchanged.
- The scheduler accounts for every archive in paths and orders conflicting writes; unrelated reads/writes can remain parallel.

## Verification

Pure-Go office/ZIP tests passed, including full text from a manifest present only in the second explicitly named archive, no extraction, malformed/binary/CRC failures, output limits and symlink handling. Checkpoint tests passed for Turn and Controller, exact undo/redo of overwritten/new entries and a custom default extraction root. Agent tests passed for native and invoke_tool dispatch, command repair/worker propagation, inert extraction, observation invalidation and conflicts on all read sources. Test fixture setup errors (missing description then inactive/incomplete target schema) were corrected before passing; those were not production failures.

BenchmarkReadZip_ReadAcrossExplicitParts, Windows amd64 / Ryzen 5800X3D / Go 1.26.2 / GOMAXPROCS=2, three trials: 2.379–2.457 ms/op (median 2.430), about 120 KB/op, 1,459 allocations. This is local ZIP processing on a synthetic fixture, not an end-to-end model speedup. Logs: ignored .tmp/optimization-oct8-2026/go-zip-tui-experiments-fixed-{0,1}.log. Live-model result, if available, is recorded separately.

## Live-model attempt

The public Zen catalog returned 200 and listed space-bunny-free on 2026-10-08. The synthetic whole-loop smoke used the production OpenAI Zen route and injected gate tools bash/read. The sole completion request returned HTTP 429 after 0.928 s, with no model text or tool calls; this does not establish model-level effectiveness. No retry storm or alternate-account workaround was performed. LM Studio 127.0.0.1:1234 was unavailable (ECONNREFUSED). Local deterministic integration and benchmarks above passed. The logged game package parts total 28,889,441 B and fit the preserved aggregate disk cap.
