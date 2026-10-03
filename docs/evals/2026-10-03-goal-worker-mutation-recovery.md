# Delegated mutations unblock stale parent command failures

Date: 2026-10-03. Baseline: main 78c5366 / dev16.

A parent allowed an identical failed command to run again after its own verified file edit, but not after the same edit performed by a task worker. The child expired only its own command-failure gate. A completed task or report cannot prove a mutation, so treating every task result as a change would also forgive unrelated failures.

The change forwards only successful, verified, non-inert mutations through an optional delegation context. Each receiving Loop checks the actual mutated workspace and its captured failed-check generation before expiring command failures. Outstanding failed-check evidence remains unresolved until the matching check actually passes.

## Reproduction and checks

A public Loop.Run fixture executes two failing checks, delegates a real PatchFile repair, then requests the identical parent check. Its ctx_execute double reads the real file but launches no child process. The unchanged baseline blocked the third execution for both task and send_message continuation. The candidate permits that check to execute and pass.

A second public fixture uses an explicit custom LoopFactory to permit one nested task. The sequence is root failed checks -> middle failed checks -> grandchild PatchFile -> middle rerun -> root rerun. Built-in workers still structurally forbid nested delegation.

| Public fixture | Unchanged baseline | Final candidate |
| --- | --- | --- |
| Task repair | 2 check executions; required rerun blocked | 3 executions; rerun passes |
| Same-worker continuation | 2 executions; required rerun blocked | 3 executions; rerun passes |
| Nested custom factory | 4 executions; both reruns blocked | 6 executions; both reruns pass |

The task and continuation fixtures retain their existing six/eight provider-double calls. The nested fixture uses exactly ten calls. No actual model request, GPU workload or user program was used. These numbers describe deterministic fixtures, not a measured reduction in historical agent turns.

Final focused agent tests passed (0.146 s), and agent vet passed. The final case-sensitive guard was checked in the same run; earlier baseline failures were not repeated.

Durable tests in internal/agent/worker_mutation_test.go also cover no-op/error/read-only work, separate worktrees, current/stale run and conversation generations, eight concurrent callbacks, background delivery, cancellation and panic, and unchanged ordinary invocation representation. Existing retry-after-edit and verification-forwarding controls were included.

The optional symlink alias control depends on host permission. Its availability was not printed by the nonverbose passing test run.

## Ownership and lifecycle

The port changes only loop_tools.go, agent_tool_helpers.go and the new worker_mutation.go.

- Only task/send_message admission creates the optional mutation observer context value and closure. worker_invocation.go remains byte-identical; its three-field representation remains four pointer-sized words (32 bytes on 64-bit).
- Actual mutation success uses the existing post-verification !Inert predicate. Completion text, no-op, failure, interrupted errors and panic do not notify.
- An observer captures its own original workspace and generation. Generation validation and command-gate expiration share failedChecks.mu, linearizing delivery with the existing Reset / LoadConversation fences.
- Nested observers retain the original upstream callback. The actual childRoot is forwarded unchanged after all local locks are released. Each ancestor applies its own workspace/generation guard; no upward callback holds a descendant failed-check mutex.
- Exact normalized root strings take the zero-I/O path. Other spellings, including Windows case-only differences, require directory metadata and SameFile. This avoids assuming case-insensitive NTFS. A valid alias uses two Stat calls; missing/non-directory alternate paths fail closed.
- Detached background workers carry the originally bound observer. They cannot recapture a newer parent generation at completion.
- Successful verified mutation racing cancellation is retained consistently with existing successful-result handling. Interrupted errors return before notification.
- Only the existing command-failure entries expire. Other failure and repeated-write gates remain unchanged; a mutation never counts as a passing verification.

The first ignored prototype enlarged the common workerInvocation struct and did not preserve nested ancestry. It was rejected before integration. The final design uses a separate optional context key and introduces no new standing Loop field, cache, schema, prompt, durable record, automatic retry or native-history transformation.

The observer context has a transient allocation/retention cost for delegation, including any ancestry explicitly permitted by a custom factory. Exact heap cost was not benchmarked. Ordinary dispatch representation does not grow.

## Limits and evidence

A legitimate post-repair verification may perform necessary work that was previously blocked incorrectly. There is no claim about saved historical model turns, token count, prompt-processing time, generation TPS or process RSS.

Portable ignored evidence is under .tmp/goal-runtime-round14-2026-10-03: first-proof contains the earlier receipts, baseline-nested-proof.txt records the new behavioral failure, candidate-final-proof.txt / candidate-final-vet.txt record final checks, and frozen-manifest.json records exact integrated hashes. final-candidate-proof.cmd uses bounded Go overlays and portable cache/temp directories.
