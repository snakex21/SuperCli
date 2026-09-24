# Avoid rediscovery after moderate batch reads

Date: 2026-09-24. Shared CLI/TUI and GUI tool-result policy. No provider transport,
OpenCode Zen special handling, permanent prompt, schema or extra inference changed.

## Evidence and implementation

A read_many result just above the old 8192-byte inline threshold was reduced
to an approximately 4096-byte preview. An inspected session had a 10191-byte
batch followed by separate overlapping reads of its source files. Across the
existing stored transcripts, 195 of 984 read_many results had retained-output
sizes in (8192,12288]; 780 had a retained-output marker. These historical logs
predate current fixes and do not prove that every later read was unnecessary.

Complete captured multi-item read_many results may now stay inline up to
12288 bytes. Eligibility requires more than one item, no failed item and no
per-item/global omission represented by RetainedText. Single-item, failed,
partially truncated or larger batches retain the existing structured preview.
Other tools retain their 8192-byte threshold. Existing per-line read bounds,
saved-output retrieval and context pruning remain intact.

The purpose is to avoid discarding specifically requested evidence only to
retrieve it on the next model turn. This intentionally trades larger individual
inputs for the possibility of one fewer inference. It is not universally cheaper.

## Controlled live comparison

scripts/batch-read-turns executes a real read_many on three synthetic files,
then starts the real agent continuation with that result. The initial tool
choice is fixed in both versions, so counts/time describe the continuation,
not an unconstrained whole task including the initial choice. Only file-read
and stored-output tools are available. Baseline source snapshots are built
with a Go overlay. Fixtures and logs stay under .tmp/batch-read-turns.

Captured result: 9632 bytes. Baseline model view: 4183 bytes; candidate: 9632.
The question asks for three values in the middle of the files; the control
asks for values near the beginning. All candidate answers were correct.
Each file/value association was inspected in addition to the runner's check.

Initial paired experiment:

| Model | Case | Calls before -> after | Seconds before -> after | Input tokens before -> after |
|---|---|---:|---:|---:|
| Muse | middle | 2 -> 1 | 10.491 -> 1.947 | 8294 -> 4340 |
| Muse | head control | 1 -> 1 | 2.212 -> 2.914 | 2680 -> 4340 |
| Qwen | middle | 2 -> 1 | 10.502 -> 6.967 | 5556 -> 4236 |
| Qwen | head control | 1 -> 1 | 4.074 -> 4.148 | 2484 -> 4236 |

Reversed candidate/baseline order and head-before-middle case order:

| Model | Case | Calls before -> after | Seconds before -> after | Input tokens before -> after |
|---|---|---:|---:|---:|
| Muse | middle | 2 -> 1 | 7.455 -> 4.353 | 8539 -> 4340 |
| Muse | head control | 1 -> 1 | 2.965 -> 2.526 | 2684 -> 4340 |
| Qwen | middle | 2 -> 1 | 13.689 -> 4.367 | 5724 -> 4236 |
| Qwen | head control | 2 -> 1 | 8.590 -> 4.665 | 5468 -> 4236 |

The pilot reused the same values under two different field names at the head
and middle. To rule out accidentally answering the middle question from the
head field, the final fixture uses different values: InitialLimit 1432/2981/6208,
BudgetLimit 7321/1847/9063. Its checker associates each value with its filename
on the same response line and tolerates additional line-number citations.

Final distinct-value comparison:

| Model | Case | Calls before -> after | Seconds before -> after | Input tokens before -> after | Correct before/after |
|---|---|---:|---:|---:|---|
| Muse | middle | 1 -> 1 | 9.314 -> 2.134 | 2682 -> 4340 | no / yes |
| Muse | head control | 1 -> 1 | 2.733 -> 2.574 | 2682 -> 4340 | yes / yes |
| Qwen | middle | 2 -> 1 | 14.785 -> 7.251 | 5654 -> 4236 | yes / yes |
| Qwen | head control | 1 -> 1 | 4.773 -> 4.080 | 2488 -> 4236 | yes / yes |

Baseline Muse incorrectly concluded BudgetLimit was absent from lines 1-70,
although it was only hidden by the preview. Candidate Muse saw all three
BudgetLimit values and answered correctly. Qwen baseline retrieved more data;
the candidate needed zero further tools.

The head control is a real downside: more input with no necessary call saved.
Cached input varied, so these raw counts are not billing estimates. Small sample
sizes, backend state, reasoning/output length and prefix caching make wall-time
figures illustrative, not a general speed guarantee. Models may still choose
redundant verification even with complete evidence. No tool call is forbidden
or silently satisfied from stale file content.

## Validation

- Regression tests fail on the saved baseline and pass with the change:
  middle evidence reaches both native and thin model routes unchanged.
- Byte-boundary tests cover 8192/8193 and 12288/12289, unchanged limits for
  read_lines/search_code/ctx_execute, and preserved saved-output handles.
- Single-item, failed-item and already truncated batches keep compact previews.
- An existing worker-evidence test had a random failure when an opaque handle
  contained "999"; its assertion now checks "AuditValue = 999" instead of every
  occurrence of the number. Production worker behavior is unchanged.
- Full go test -timeout=90s ./... and go vet ./... passed after production changes.
  Final runner-only validation/builds, startup checks and installation are saved
  with the evidence.

All pilot results, final distinct-value results, before/after regression output,
full tests, baseline source overlay and installation hashes are retained in
.tmp/batch-read-turns/. comparison.json is an exploratory summary; one pilot
row was falsely flagged by its strict numeric adjacency check because the
correct response also cited line 35. The raw response was manually verified;
the final runner accepts line-number citations.
