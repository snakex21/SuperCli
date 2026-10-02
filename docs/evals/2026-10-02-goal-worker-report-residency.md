# Goal evaluation: completed-worker report storage

Date: 2026-10-02. Audit baseline: main `3eae0a5`.

## Change

A completed worker previously retained its final report twice: the Loop stored the assistant reply for later continuation, and the worker separately accumulated streaming text into `LastResult`. After a successful run, `canonicalWorkerReport` now reuses the immutable text already held by the latest assistant message, only when its trimmed value equals the final report exactly. It accepts plain Content or one text part alongside native reasoning parts.

The report bytes, transcript, native continuation payload, tool registry, evidence, token budget and worker lifecycle remain unchanged. Empty results, mismatches, tool calls, unsupported/multiple text parts and steering diagnostic suffixes keep their existing representation. Failed and stopped runs bypass canonicalization. The helper runs after the Loop channel closes while the worker run lock is held. There is no new cache, persistence format, prompt, model call, service or dependency. The Zen path is unchanged.

This saves duplicate retained report storage. It does not reduce the number of agent turns, token input or the required canonical conversation history.

## Why finished Loops cannot simply be unloaded

Child loops deliberately do not inherit the parent conversation Writer. `TestWorkerRetainsOutputWithoutSharingConversationWriter` enforces that separation. Large tool outputs are shared and can use portable persistence, but the child conversation, hidden-message mask, native reasoning state, discovered restricted registry and frozen context prefix are not a durable worker snapshot. Restoring only existing tool-output rows would lose continuation state.

The nearby Codex implementation in `codex-main/codex-rs/core/src/agent/control/residency.rs` unloads only terminal, idle threads with an empty mailbox. Before removing a thread, it calls `ensure_rollout_materialized()` and `shutdown_and_wait()`; failure keeps the thread resident. This is a materialization and lifecycle mechanism, not a retention-limit adjustment that can safely be copied into SuperCli. A broader unload/reload change would first need an equivalent durable worker snapshot. This round does not remove workers, change worker retention limits or discard their histories.

## Read-only stress fixture

An ignored Go overlay exercised the production task tool with a scripted in-process streaming provider. Twenty completed workers each received a generated report in 2,048-byte chunks. Context windows were large enough to avoid compaction. After GC, the fixture measured retained heap, replaced only duplicate report references by exactly equal canonical references, ran GC again, and continued all twenty workers through the production worker loop. No provider network, model or GPU was used.

| Fixture report size | Workers | Final report bytes retained | Heap before sharing | Heap after sharing | Heap freed in this run | Successful follow-ups |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 64 KiB | 20 | 1,310,700 | 4,154,904 | 2,760,600 | 1,394,304 | 20/20 |
| 1 MiB | 20 | 20,971,500 | 46,773,712 | 25,609,856 | 21,163,856 | 20/20 |

Twenty duplicate reports shared canonical storage in each case, and each follow-up still received the full previous report. Child Writer count stayed zero. The serialized retained histories were 1,409,420 and 22,299,020 bytes respectively; those histories remain necessary and were not removed. Final report totals exclude trimmed trailing whitespace. GC heap figures include allocator rounding and surrounding retained fixture objects, so the exact heap delta is not a deterministic API guarantee.

These are deliberately large synthetic reports, not a prediction of ordinary GUI process RAM savings. A read-only aggregate query of available parent-session tool rows found 76 task/send_message results, a maximum of 8,520 characters and 216,143 characters in total. These parent views may be compacted previews and are not raw worker LastResult measurements. They cannot establish typical real-world savings. Private IDs, messages, arguments and paths were not exported.

The pre-implementation overlay and completed measurement output remain in the ignored `.tmp/worker-residency-audit-2026-10-02` directory. Permanent production tests verify that the new path already shares storage without the fixture replacement step.

## Validation

Passed:

- Exact result equality and shared backing storage for Content and a single text part with native reasoning; history JSON remains byte-identical.
- Thirteen fallback cases plus a nil Loop retain the original result storage: no assistant/history, newest reply mismatch, newest tool call, multiple text parts, image/unknown part, reasoning-only message, Parts precedence, thinking mismatch, steering suffix, untrimmed report and empty result.
- Production task plus send_message continuation for plain and native-reasoning replies: full prior history and native payload preserved, one worker and exactly two provider requests for the two runs.
- A failed stream with a partial report identical to an older assistant reply retains its partial result and error diagnostics without incorrectly sharing old reply storage.
- Related worker, send_message, steering/cancellation, retention/recovery and native-reasoning continuation regression tests.

Commands:

```text
go test ./internal/agent -run "^(TestCanonicalWorkerReport|TestWorkerSharesFinishedReportAndPreservesContinuation|TestFailedWorkerDoesNotAliasCoincidentallyEqualOldReply)" -count=1 -v
go test ./internal/agent -run "(Worker|SendMessage|NativeReasoningSurvivesLiveAndResumedTurns)" -count=1
```

No stage, commit or executable build was performed by this worker.
