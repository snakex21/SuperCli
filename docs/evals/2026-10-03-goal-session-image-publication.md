# Complete session-image publication

Date: 2026-10-03. Baseline: main 306ff56 / dev13. Scope: session media storage, deterministic filesystem controls and matched CPU/I/O fixtures. No model/API requests, actual screen capture, user configuration changes or private transcript exports.

## Defect and resulting behavior

The old writer created the final digest filename before writing its contents. A concurrent externalization of identical bytes saw an existing path and returned success immediately, even if the file was empty or incomplete. An interrupted previous write left the same permanently accepted state. The overlay reproduced successful refs to 0-byte, 5-byte and oversized files, plus a zero-byte final path held open by the first publisher. These tests failed against the baseline. This proves an unsafe state transition; it does not establish its frequency in saved sessions or connect it to inference speed.

New writes use a unique temporary file in the same session-media directory. After the complete write and close, the file is published under the original digest filename. Existing complete regular files reuse their path without a pixel read or disk rehash. A short/oversized existing regular file can be repaired from the supplied original bytes; directories and symlinks fail closed. Failure removes only this call's temporary file and returns the storage error unless another publisher has completed the final file. Existing agent fallback retains the original pixels inline on storage failure.

One constant Store.mediaMu protects the metadata/publication/repair decision. Input SHA-256 hashing stays outside the guard. The Store already contained a mutex, uses pointer ownership, and passes copy-lock vet checks. No image cache or per-path lock table was introduced.

## Windows publication review

An initial replacement-only prototype exposed another real race: concurrent duplicate writers could replace a complete Windows target while the successful first caller opened it, causing a sharing error. Fresh-target publication now uses MoveFileEx without replacement; a complete winner is reused after a name collision. Replacement is used only for an existing incomplete regular target under the Store guard. A complete-file dedup control verifies the original file identity remains unchanged.

MOVEFILE_REPLACE_EXISTING permits file replacement and rejects directory targets. WRITE_THROUGH concerns completing/flushing the move; it is omitted here, and this change makes no power-loss durability claim. The temporary and final paths share a directory, and COPY_ALLOWED, delayed-reboot moves, registry writes, truncate and remove-then-rename fallbacks are not used. See the [Microsoft MoveFileExW contract](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-movefileexw). Non-Windows publication uses same-directory os.Rename.

## Validation

- Baseline overlay: four deterministic partial/in-flight cases FAIL; candidate tests PASS.
- Full internal/storage/session tests: PASS (5.167s), including existing deletion, persistence and resume controls.
- Agent ToolImageRef / ToolImageExternalize regressions: PASS (0.161s), including original-byte fallback and durable provider-wire behavior.
- Thirty repetitions of actual eight-writer controls: fresh and repaired files within one Store, fresh publication through independent Store handles, held-open first-publisher state, and a deliberately locked incomplete Windows target: PASS (1.145s). Successful refs are read immediately and compared byte-for-byte. Failure controls verify no leaked owned temporary, unchanged locked partial file, and successful repair after releasing its handle.
- Complete dedup identity, directory/symlink rejection, failed replacement preserving a valid final file, legacy inline repair and portable-directory move: PASS. Symlink creation is skipped where OS privileges do not permit it.
- A Windows portable path longer than 260 characters: PASS locally for publication and dedup; no settings changes or speculative path-normalization patch.
- Scoped session vet and diff check: PASS.

## Measured tradeoffs

Matched source-baseline, final publication without the mutex, and final production variants ran serially on Windows amd64 / Ryzen 5800X3D, GOMAXPROCS=2. Each arm used three samples of 64 operations per size, with order alternated. Small synthetic bytes represent file storage only; no decoder/GPU/network was exercised.

| Operation | Baseline median | Final median | Allocations | Bytes allocated |
|---|---:|---:|---:|---:|
| Existing 1MiB | 656.172µs | 623.494µs | 22 → 22 | 3496 → 3560 |
| Existing 8MiB | 4.357ms | 4.280ms | 22 → 22 | 3496 → 3560 |
| New 128KiB | 2.486ms | 2.387ms | 24 → 33 | 3848 → 5960 |
| New 1MiB | 1.186ms | 1.596ms | 24 → 33 | 3848 → 5960 |

The extra temporary-file/publication work has a measurable new-file cost; in the final 1MiB fixture it was about 0.410ms and 2.1KB of transient allocation. Earlier prototype medians increased by 0.5–0.7ms; 128KiB filesystem timings changed in both directions across samples. Do not infer a speedup from the faster medians. Good-file dedup allocates only about 64 additional metadata bytes and retains no additional pixel copy.

Removing only the mutex gave existing-file medians of 623.542µs / 4.282ms, compared with 623.494µs / 4.280ms in production; allocation counts were identical. Uncontended lock cost is below the noise of these fixtures. Concurrent repair/publication through a single Store is deliberately serialized for correctness; the serial benchmark does not measure contention throughput.

There is no claimed reduction in input tokens, model calls, inference latency, process RSS or historical retries. This is a correctness fix with an explicit I/O/allocation tradeoff.

## Boundaries

Expected-length validation catches incomplete old in-place writes; it does not detect externally changed same-length contents. No new fsync/power-loss guarantee is added. A crashed process can leave an unpublished temporary file, which session deletion removes with the media directory. File-backed old history without the original bytes cannot reconstruct previous corruption. Separate processes/Stores attempting to repair the same already-incomplete file are outside the single-Store repair guard; no cross-process lock machinery was added. Fresh independent-Store publication is covered by the concurrent test.

Other R11 runtime paths already cleared retired persistence-queue references, bounded worker/output retention, shared finished report bytes and reused decoded resume history. They did not justify another cache or loop-metric change. Read-only tools may observe external mutations, so generic duplicate-read suppression was not added.

Exact before/after overlays, completed benchmark receipts, failure controls, test logs and source hashes are under the ignored .tmp/goal-runtime-round11-2026-10-03 directory. No live model check is necessary for this filesystem-only change.
