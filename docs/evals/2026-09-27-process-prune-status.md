# Managed-process status survives context pruning — 2026-09-27

## Confirmed defect

`pruneMarker` preserved exit status only for `ctx_execute`. A completed `process_session` result became `[tool result pruned: process_session; details omitted]` (or a marker carrying only an output reference). Process identity, terminal status and exit code disappeared from the model's working history. Large successful process results also used a generic cut-JSON preview, preventing reliable status extraction.

Eight real-process red cases reproduce the loss: native/thin dispatch, exit 0/7, and moderate/large output. The helper cannot finish until a local socket handshake releases it; the test then uses one blocking process wait. No process-status polling or timing sleeps are used. This is a code-backed defect, not an assertion that it caused the earlier Muse fixture's repeated `ctx_execute` call (that uses a different path).

## Change

- Individual process results retain a bounded `id=proc-N, status=..., exit_code=N` marker when metadata is available. Running snapshots never acquire an exit code; stopped/failed/timeout remain distinct.
- Large non-error process snapshots get a valid JSON model preview within the existing 4 KiB budget, preserving identity/state/duration, both stream endpoints, omission flags/counts and PTY state. The output store keeps the original snapshot with command/workdir. Small results, failure diagnostics, UI snapshots and capture limits retain their behavior.
- The JSON-string truncation primitive already used by command previews is shared through `core.PreviewJSONString`; its escape/rune boundaries and byte budgeting are unchanged.
- Pruning accepts only complete JSON (optionally followed by a validated store footer) or the tool's generated failure header. It does not infer process outcomes from log text, malformed/truncated JSON, arbitrary IDs or unrelated file results.

The history marker describes the observed snapshot, not a new check of the current process/workspace. Older generic cut-JSON previews remain unknown rather than being guessed. Listing multiple sessions is not treated as an individual command result. No automatic retry, test suppression, extra provider call, persistent instruction or storage location was added.

## Evidence and limits

After the fix all eight real-process cases keep the process outcome through pruning and `runWorkerLoop` continuation. Each starts once and waits once; preserved output handles remain readable. The resulting fixture markers are 91–139 bytes instead of losing metadata, and no model inference is used to summarize them. The complete original tool view is 2.2–4.7 KB in these cases.

Additional tests cover 48 status/stream encoding combinations (Unicode, control characters, escaped JSON, large metadata), preview eligibility, all existing command-preview boundaries and invalid status evidence. No live-model saved-turn or latency reduction is claimed: the verified improvement is that the agent receives the existing outcome without another process lookup or command execution.

## Validation

- Red/green real-process and resumed-worker regressions: pass after the change, failed before it.
- `go test -timeout=90s ./...`: passed.
- `go vet ./...`: passed.
- CLI and GUI builds: passed; CLI `--help` smoke passed.
- `git diff --check`: passed.

Evidence/builds are under `.tmp/process-prune-status-2026-09-27/`. The installed manifest records hashes and backup paths. This batch does not change Zen transport or provider selection. Runtime work is limited to large process previews and existing context-pruning passes; small outputs incur only the eligibility check.
