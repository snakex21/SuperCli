# Explicit source reads after discovery — 2026-10-12

## Problem and change

An agent explicitly requesting `read_lines` received an `already shown in tool result` marker instead of the requested source. The source projection deduplicated all recognized reader outputs, including targeted rereads. A previous search or batch read could therefore make an explicit reread ineffective.

`sourceEvidenceProjection` now preserves `read_lines` and `read_output` content. This applies to direct calls and calls wrapped by `invoke_tool`, including its flat argument form. These complete results still contribute line hashes, so subsequent discovery can reference the latest complete visible result.

Searches and batch reads retain deduplication. The projection still rebuilds references from the exact visible request, so hidden, compacted or pruned evidence cannot justify an omission. The canonical transcript is unchanged. Existing repeat-loop warnings and stops remain active; explicit access to source does not permit an unbounded discovery loop.

This changes the shared provider-request preparation, with no model-specific branch and no changes to reasoning settings. Normal tool size limits and actual context-window safeguards still apply.

## Verification

Before changing production code, the new tests failed in all four projection cases and all six main/worker protocol cases with the reported marker in place of source lines 65–68.

After the fix:

- Explicit reads following a search return the requested text, including a second explicit reread.
- A later batch discovery references the latest complete visible read.
- Actual `read_many` and `read_lines` tools preserve this behavior in the main loop and in task workers, through native, sentinel and dispatcher calls.
- Existing overlap, unchanged-prefix, hidden/pruned/compacted evidence, fresh edits and repeat-loop tests pass. Their discovery fixtures now use `read_many`, since explicit `read_lines` intentionally no longer deduplicates.
- `go test -p 1 ./internal/agent/... ./internal/llm/...` passed.
- `go vet -p 1 ./internal/agent/... ./internal/llm/...` passed.
- `go build -p 1 ./...` passed. TUI and GUI binaries were rebuilt, smoke-tested (`--version` / `-h`) and atomically installed with backups in the same portable staging folder.

Logs, portable Go temporary data and source-hash manifests are under `.tmp/explicit-source-read-2026-10-12/`. No paid provider calls were needed for this deterministic request-content regression. The tests inspect the requests delivered to the scripted provider, rather than relying on the saved transcript alone.

## Files and scope

- `internal/agent/source_evidence.go`: preserve explicit and recovery reads; record them as complete visible evidence.
- `internal/agent/source_explicit_read_test.go`: new regression tests and batch fixture helper.
- `internal/agent/source_evidence_test.go`: retain meaningful generic discovery coverage.
- `internal/agent/source_evidence_loop_test.go`: explicit rereads stay complete while loop guards remain effective; missing-reference coverage retained.

The other agent's partial advisor implementation was preserved. This patch does not complete `advisor_model` configuration or its application/GUI wiring, and does not claim advisor evaluation is complete. No commits or releases were made.

The source-hash manifest was checked before and after builds, including the advisor edits. Installed SHA-256 values are recorded in `.tmp/explicit-source-read-2026-10-12/installed.json`. Already-running processes were not stopped; restart SuperCli to use the updated executable.
