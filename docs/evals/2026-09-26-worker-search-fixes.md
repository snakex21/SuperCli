# Worker context and search fixes — 2026-09-26

## Evidence and changes

1. **Oversized search context.** GunMayhem session `de105c910d16e47e`, message 1127 in the fixed September 25 snapshot, rejected `search_code` with `context: 25` before searching. The display radius now clamps to 20 and the result says it was limited. Negative values still fail; automatic context, `context: 0`, match limits, line budgets and cancellation retain their behavior. This avoids the corrective tool/model round caused solely by that display parameter.

2. **Shared worker history.** The direct/legacy `share_context` path copied the canonical parent archive, including hidden messages. It now takes `VisibleMessages()`, excludes the parent's system prompt as before, and retains current requests and completed tool/result pairs. The parent's archive is unchanged. This flag is not exposed in the current public task schema: normal isolated workers still start without parent history. This is a compatibility-path fix, not an explanation of every slow delegation. Existing provider-side tool-pair repair already handles incomplete exchanges and was not changed.

3. **Read-only worker instructions.** The review, plan, explore and advisor roles inherited an instruction to fix errors despite lacking mutation/command tools. Their shared instruction now asks for findings, suggested checks and blockers and states the existing no-shell/no-edit restriction. It replaces 146 characters with 136; code/general worker instructions are unchanged. No tools or permissions were added. The prior architecture review reported three attempts to use unavailable `ctx_execute`; the instruction repair addresses that mismatch, but its effect on live model behavior has not been measured.

## Validation

- Both new regressions failed before the code changes and passed afterward.
- The search regression executes through the real registry on a fixture, verifies identical match evidence and exactly 41 displayed lines for oversized radii, including the largest platform integer. Existing tests cover the 500-line aggregate cap, fallback/rg parity, long lines and cancellation.
- The worker regression includes a hidden tool exchange and a current complete exchange. It checks that current evidence survives and canonical history remains byte-identical. On this deliberately large fixture, estimated seed tokens change from 73,451 to 94. These are estimator values for synthetic data, not a live session saving or billing measurement.
- Full `go test -timeout 90s ./...`, `go vet ./...`, and `git diff --check`: passed. An initial validation runner timed out without saving results; after awaiting process exit, the checks were rerun with durable result capture and completed successfully.
- CLI and GUI builds: passed.

Raw local evidence and verification records: `.tmp/worker-search-2026-09-26/`.
The common agent/tool layer applies these changes to local and cloud models, without additional model calls. There was no live provider benchmark and no change to the special Zen transport.

Both root executables were installed after the CLI help smoke check, with backups
and verified SHA-256 records under the local evidence directory. Running instances
use the new build after restart.
