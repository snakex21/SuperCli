# Command-result presentation experiment — 2026-09-27

## Question and setup

An earlier Muse coding replay reread source and repeated an already-passing test before reaching the evaluation's eight-request ceiling. The saved result contained the successful exit code and complete test output. Removing the code-worker completion hint had already failed to demonstrate a benefit.

This comparison changes only an opt-in test path: SUPERCLI_CODING_COMMAND_TEXT=1 rewrites small, complete successful ctx_execute results at the provider boundary to a status line plus verbatim decoded stdout/stderr, command, workdir and duration. Execution, verification and stored messages keep the original result. The corresponding tool description states the experimental format. Large/partial/error results are unchanged. Actual provider-facing command views are captured in the evaluation artifact.

Both models ran the existing synthetic cache-expiry editing fixture with thin tools, deferred schemas, written patch snapshots and the unchanged implementation hint. Existing test files must remain identical; an independent Go test checks the change. No private project content was sent to providers.

## Results

| Model | Format | Model requests | Input tokens | Worker time | Edit, tests and report correct |
| --- | --- | ---: | ---: | ---: | --- |
| Qwen 3.8 27B, LM Studio | existing JSON | 5 | 18,941 | 30.309 s | yes |
| Qwen 3.8 27B, LM Studio | text prototype | 5 | 19,099 | 32.592 s | yes |
| Muse Spark 1.2 free, Zen | existing JSON | 5 | 20,470 | 14.101 s | yes |
| Muse Spark 1.2 free, Zen | text prototype | 6 | 24,810 | 15.245 s | yes |

Order: Qwen/JSON and Muse/text together, then Qwen/text and Muse/JSON together; models do not share a serving backend. One sample per variant/model is insufficient to infer a general speed ranking. The text Muse run additionally reproduced the initial failure; that is useful verification, not itself a redundant retry. None of these four runs repeated a passing check or reread source after verification.

## Decision

Keep production JSON unchanged. The alternative did not reduce calls or total input in this comparison. Fresh results also show that the earlier repeated-call behavior is not deterministic. Captured request views confirm successful test evidence reached the next provider call.

The opt-in experiment and its transformation safety test remain available; they are compiled only into tests. Production instructions, result formatting and Zen handling are unchanged. A separate verification-identity defect discovered from the verbose/nonverbose command pairs is covered in 2026-09-27-verbose-check-recovery.md; it does not explain the earlier repeat because that worker did not call the goal tool.

Artifacts: .tmp/worker-completion-format-2026-09-27/{qwen-json,qwen-text,muse-json,muse-text}/deferred.json and execution logs. All four isolated coding checks pass; full test/vet/build validation also passes.
