# Report all-failed read batches as failures

Date: 2026-09-26. Shared read_many tool and agent event/result handling.

## Evidence

The fixed trace contains read_many summaries with 0 ok / 4 failed and 0 ok / 5 failed (sequence 665 and 1371). They include historical range errors already addressed by the earlier range-cap fix. Inspection of the current renderer showed a remaining general defect: every per-item-error batch still returned Result.Err=nil, even if no file was read. The nonempty diagnostic text passed the generic read verifier and was recorded as a successful observation.

A current real-file fixture with two absent files reproduced this through native and thin dispatch: both the agent outcome and UI ToolResultEvent indicated success. A pre-cancelled batch also lost its cancellation cause. These are code-backed regressions, not proof that the historical status caused a particular rerun or the long session.

## Change

When all requested items fail, read_many now sets Result.Err. The error preserves each original cause for errors.Is, including context cancellation and missing files. The model receives the existing numbered item diagnostics with the existing 4 KiB structured-preview bound, rather than a generic tail that could discard early errors. Full UI output and omitted diagnostics remain available through the existing retained-output mechanism. Small errors are not duplicated.

Mixed-success batches keep their successful contents and explicit per-item errors; wholly successful reads stay unchanged. No repeated file access, automatic alternative-path read, new model request, tool-description growth or provider/Zen change. Failed batches are no longer reported as successful tool observations. No end-to-end speedup claim.

## Validation

- Red/green tests for two missing files, a fully cancelled batch, and a twelve-item long Unicode diagnostic batch.
- Both dispatch routes mark the outcome failed, suppress a successful observation, preserve the model error and emit a failed UI event with the original diagnostics.
- All twelve numbered diagnostics stay visible in the bounded preview; full omitted detail is recoverable by read_output.
- Existing partial-success, range-cap, grouped-read, preview and read_many tests pass.

The investigation found no identical ranges within saved read_many calls. Most overlapping ranges share only one boundary line; there was no basis to add a new deduplication format. Three wrong-root path failures in a later batch were followed by usable existing command evidence, so no automatic path rerouting was added.

Artifacts: .tmp/read-many-failure-2026-09-26/{before,after,retrieval-test,snapshot-evidence}.json.

Full go test ./... and go vet ./... passed. CLI/GUI builds and CLI --help smoke passed. Installed executables match their build hashes; previous binaries are backed up under .tmp/read-many-failure-2026-09-26/before. Restart running instances to use the fix.
