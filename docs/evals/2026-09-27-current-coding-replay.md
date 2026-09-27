# Current-version coding replay

Two isolated copies of the existing cache-expiry fixture were run through the real code-worker loop, thin protocol, deferred schemas and written patch snapshots. The production source was unchanged during this experiment. Existing test files must remain byte-identical; an independent go test ./... checks the resulting implementation. Only synthetic files were sent to providers.

| Backend | Model requests | Input tokens | Worker time | Result |
|---|---:|---:|---:|---|
| Qwen 3.8 27B, LM Studio | 5 | 18,690 | 30.201 s | Correct edit, passing checks, unchanged tests, final report |
| Muse Spark 1.2 contributor free, Zen | 8 | 34,967 | 15.702 s | Correct edit and passing checks, unchanged tests, but no final report before the eval's eight-request ceiling |

Neither run hit the evaluation command gate. The local run batched listing/search and the two source reads, edited once, checked once and reported. The cloud run listed, read the source/tests, reproduced the test failure, reread the source, edited, passed the tests, reread the edited source and repeated the identical passing test command. There was no intervening file mutation or injected verification rejection after the first passing check. The repeated test returned Go's cached result.

The cloud result is therefore incomplete under the harness's report requirement, even though independent code verification succeeds. This is not a production eight-step limit: that ceiling is specific to this bounded evaluation. Raising it alone would not remove the extra reads/check. Do not claim all models have stopped repeating work or that these uncontrolled provider timings prove a speedup.

The continuation now has a current trace of the remaining behavior rather than relying only on the older GunMayhem log. Next investigation: whether duplicate completion guidance or the presentation of completed verification contributes to these extra calls, using an opt-in controlled comparison before any production prompt change. Existing successful checks must not be suppressed solely because their command text repeats.

Artifacts: .tmp/current-coding-replay-2026-09-27/{qwen,muse}/deferred.json and sibling execution logs. No new app binaries were required: the just-installed worker-probe-cancellation builds contain the tested production source.

Follow-up: completion-hint removal (2026-09-27-worker-hint-ab.md) and the command-result text prototype (2026-09-27-command-result-presentation.md) did not demonstrate an improvement, so both production behaviors remain unchanged. The latter four trials all completed without repeating a passing check. A separate verbose/nonverbose Go test identity defect was reproduced and fixed (2026-09-27-verbose-check-recovery.md); it was not the cause of this trace because this worker never invoked goal completion. Further repetition work should use a new representative trace rather than repeat these same comparisons.
