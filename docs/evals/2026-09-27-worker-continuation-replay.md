# Live multi-file worker continuation replay

## Scope

The opt-in TestWorkerContinuationWorkflow_Live creates a synthetic Go project under the app's .tmp directory. A code worker repairs a queue expiry boundary and whitespace normalization in publishing. The evaluator then adds a new failing test for empty keys and continues the same worker with send_message. Each stage independently runs go test ./... and checks that the supplied tests remain byte-identical. No private project content is sent.

Both providers used thin tools, deferred optional schemas and stable toolsets. The fixture used the agent's 16,384-token fallback context window and a 16-request limit per stage; these are evaluation settings, not a claim about the user's configured model limits. Subsequent test artifacts also record the effective window and pruned-result count per request to make this constraint explicit.

## Recorded results before the discovery fix

| Model / stage | Requests | Tools | Input tokens | Worker time | Outcome |
|---|---:|---:|---:|---:|---|
| Qwen 3.8 27B / initial | 5 | 6 | 22,431 | 79.532 s | Correct code, tests unchanged, checks pass, final report |
| Qwen / continuation | 4 | 3 | 28,387 | 49.952 s | Correct code, tests unchanged, checks pass, final report |
| Muse Spark 1.2 free / initial | 8 | 7 | 40,744 | 26.456 s | Correct code, tests unchanged, checks pass, final report |
| Muse / continuation | 16 | 16 | 177,780 | 49.763 s | Correct verified code, but no final report before the evaluation limit |

There was exactly one child Loop per model and two runs of that worker. Every continuation request retained the initial task and follow-up instruction; the first continuation also retained earlier tool exchanges. No evaluation command gate rejected a call.

Muse's continuation contains repeated discovery, rereads and three successful test commands after its single production edit: the second test is identical to the first, the third adds -v. It also emits one empty invoke_tool call, which fails normal argument validation. It does not call goal completion, so a completion verification rejection cannot explain this trace.

The final saved history contains pruned results. The recorded request tool-token cost first drops visibly at call 14, after redundant discovery/reads had already begun. The first trial did not capture per-request prune counts, so this is not a complete causal isolation of pruning. The fallback 16k window and uncontrolled provider behavior are important limits. These runs do not prove model-wide speed differences or eliminate the repetition problem.

## Actionable finding

The final query "run go test build check" returned ctx_execute, edit_docx and list_dir, including an unrelated large Word schema. The lexical ranker matched "run" in both command execution and Word text-run descriptions, but did not connect "test/build" to "tests/builds" in the execution description.

This led to a deterministic discovery regression and a narrow fix documented in 2026-09-27-tool-discovery-command-forms.md. That result occurred at the last allowed step, so its output-size reduction is not presented as input-token savings already realized in this live run. Earlier repeats remain an open investigation; increasing the step limit or suppressing passing checks is not a fix.

Artifacts: .tmp/worker-continuation-replay-2026-09-27/{qwen,muse}/result.json and sibling execution logs. Only the evaluator is new; production worker prompts, model policy and special Zen behavior remain unchanged.

Measurement scope clarification: these live thin fixtures used the supported tail catalog placement. GUI/TUI defaults already hoist a stable catalog; see [production-profile follow-up](2026-09-27-worker-production-profile.md) for the corrected evaluator and live checks. Within-variant comparisons above remain scoped to their recorded placement.
