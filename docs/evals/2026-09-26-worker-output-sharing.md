# Worker output references share the live session cache

Date: 2026-09-26. Shared agent path, no provider or Zen changes.

## Evidence

Code inspection found that restrictedRegistry created a new OutputStore and deliberately rebuilt read_output against it. Workers inherited optional output persistence, so the existing persistence-backed test passed. Without persistence, or after a failed save, a handle in the worker report referred to an entry only in the worker cache. The parent immediately returned unknown/expired handle or a persistence failure despite the worker still holding the result. This is a reproduced implementation defect, not evidence that it caused the earlier two-hour session.

Eight red/green integration fixtures cover native/thin parent dispatch, no persistence/failed writes and matching/distinct tool-base registries. All eight failed before and pass after. The worker performs one source operation and two scripted model requests (read and report); the parent retrieves the existing detail with zero persistence reads. No real-model saved-turn or latency claim.

## Change

NewRegistrySharingOutputs creates independent empty tool/discovery registries linked to one bounded OutputStore. Worker construction uses the active parent registry as the evidence source, even when BaseRegistry separately supplies allowed tools; without a parent it uses BaseRegistry. read_output is still rebuilt for the child. Tools, schemas, activation, conversation history, writers and persistence contexts remain independent. Only immutable retained outputs and their in-memory LRU are shared.

This preserves parent-to-worker, worker-to-parent and sibling handles while resident, without copying transcripts, duplicating full logs, adding model calls or extending tool descriptions. A healthy persistence backend also avoids a redundant read on a warm family reference. Separate sessions get separate stores; a fresh registry can still restore the same handle from its explicitly provided persistence.

## Bounds and tradeoff

The existing 32-entry / 16 MiB limits now apply to the parent/worker family together instead of multiplying per worker. This can evict an older in-memory-only handle earlier when several workers produce logs. Handles remain explicitly temporary/bounded; persistence restores evicted results when available. Tests verify shared limits, not unlimited retention. No extra app data location is introduced.

## Validation

- Eight integration regressions: parent retrieves the original worker detail without rerunning the source tool; no failed persistence read is attempted for live evidence.
- Existing durable worker output and restricted-tool/discovery tests pass.
- Core tests cover bidirectional/sibling retrieval, isolation from unrelated registries, no inherited tools/activation, one durable save with zero warm backend reads, fresh-registry restoration, eight concurrent writers/readers and the global item/byte cap.
- A race-detector run was unavailable in this environment: CGO_ENABLED=0 and gcc is not installed on PATH. Concurrent behavior is covered by ordinary Go tests, without claiming race-detector coverage.

Artifacts: .tmp/worker-output-sharing-2026-09-26/{before,after,isolation-tests}.json.

Full go test ./... and go vet ./... passed. CLI/GUI builds and CLI --help smoke passed. Installed executables match their build hashes, with previous binaries backed up under .tmp/worker-output-sharing-2026-09-26/before. Restart running instances to use the fix.
