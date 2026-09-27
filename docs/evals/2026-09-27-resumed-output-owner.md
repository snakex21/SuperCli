# Keep output persistence bound to the resumed session

Date: 2026-09-27

## Confirmed failure

NewLoop defaulted ToolOutputs to its original Writer. ResumeConversation later replaced the conversation writer but left that resolved output store unchanged. The interactive TUI uses this in-place resume path. Newly retained tool results, outputs from workers spawned after the switch, and pruning archives were therefore saved under the previous session ID.

The shared warm cache masked the error. A cold registry could read either session's opaque handles from SQLite, but deleting the previous conversation cascaded to the new conversation's wrongly owned outputs. Deleting the current conversation instead left those outputs in the previous session.

Twelve current-code regressions failed before the fix: native/thin calls × ordinary result/new worker/pruned inline result × deletion of the previous/current session. Each test closes and reopens its temporary database and uses a fresh registry, so cache reuse cannot hide the ownership error. This is a deterministic reproduction, not a claim that a specific saved user session repeated work for this reason.

## Change

Loop remembers whether output persistence was defaulted from Writer or explicitly supplied through ToolOutputs. After a successful in-place conversation switch, a defaulted store follows the new writer. If the new writer lacks output persistence, the former binding is cleared and outputs remain memory-only. A subsequent durable writer restores the default binding.

Explicit worker/embedder stores retain their configured owner, including when the explicit value happens to equal the initial writer. Configuration intent is tracked directly rather than comparing arbitrary interface values. Existing worker loops with explicitly inherited stores are not rebound by a different parent conversation switch.

The update occurs only after the existing resume guards succeed. Failed/canceled/busy switches do not change either owner. New workers created after a successful switch inherit the updated parent store.

## Verification

- All twelve ownership/deletion/cold-database regressions pass after the change.
- One original tool execution remains one. New-worker variants still use exactly two scripted provider requests; direct and pruning cases use none.
- Guard tests cover initially absent/transcript-only/durable writers, durable → transcript-only → durable switches, explicit stores including a non-comparable implementation, and six rejected resume cases.
- Full go test -timeout=90s ./..., go vet ./..., CLI/GUI builds and CLI --help pass. Existing TUI session-recovery, GUI output-resume and worker retention tests are included in the full suite.

## Cost and scope

One construction-time flag and one successful-resume assignment replace the stale binding. Tool invocation, model prompts, output content, storage formats and the special Zen transport remain unchanged. No additional model request, filesystem operation or database write is added. No live latency gain is claimed.

Previously misowned or already deleted outputs are not migrated or reconstructed. Bounded retention/expiry continues to apply. Tests modify only temporary portable databases under the application workspace.

Artifacts: .tmp/resumed-output-owner-2026-09-27/ contains source-before copies, red/green/guard results, full validation, built executables and installation hashes.
