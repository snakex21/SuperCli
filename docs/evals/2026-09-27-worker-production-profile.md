# Worker continuation: production tool/cache profile — 2026-09-27

## Finding

Inspection of the recorded post-check requests shows successful stdout, exit_code=0 and written patch snapshots reaching the next model request. There is no new reflection/completion instruction asking the model to continue after success. Repeated inspection/test calls in that fixture are model choices, not evidence that the harness lost the passing result.

A separate measurement mismatch was confirmed: the standalone coding/continuation fixtures constructed LoopConfig with ThinTools and StableToolset, but left CatalogHoist at false. GUI, TUI and batch all use execution.Resolve, which enables hoisting for stable thin tools. The worker correctly inherits that profile. The historical fixture therefore represented the supported thin-tail variant, rather than the default production placement.

This does not invalidate within-variant comparisons or deterministic tool regressions. It limits how directly their turn counts/latencies can be applied to default GUI/TUI sessions.

## Evaluator correction

The continuation evaluator now resolves its default tool/cache settings through the same execution.Resolve function as the applications. It records effective Thin, StableToolset and CatalogHoist flags and checks that the created worker receives them.

Explicit SUPERCLI_CONTINUATION_PROTOCOL=thin|native overrides remain available (empty/profile uses the resolver). SUPERCLI_CONTINUATION_CATALOG=tail reproduces the older fixture; hoist explicitly selects the supported hoisted variant. Invalid/incompatible choices are rejected. There are no production configuration or prompt changes in this work.

The existing real-serializer/HTTP-fixture continuation test now also covers profile-default for both OpenAI-compatible chat and Zen Responses, with completed reasoning retention both on and off. All 16 combinations preserve tool definitions, history, call/result evidence and native continuation state. The default-profile cases also check inheritance into the worker. Zen route/header checks remain in place.

## Live verification

Same successful initial history per model, synthetic Go workspace, same-worker send_message continuation, 65,536-token window and current production tool descriptions. Only the follow-up calls use the external provider. Actual request views confirm the 2,560-byte combined worker/system/catalog prefix remains at message index zero through the run, rather than moving behind each new tool result.

| Model | Follow-up calls | Tools | Input tokens | Worker wall time | Correct |
|---|---:|---:|---:|---:|---|
| Muse Spark 1.2 free | 5 | 4 | 43,455 | 18.163 s | yes |
| Qwen 3.8 27B, LM Studio | 4 | 3 | 27,419 | 56.650 s | yes |

Both runs have zero failed tools or command-gate rejections, leave supplied tests unchanged, pass independent Go tests, and finish with a report. Qwen reads only the newly supplied test, patches using earlier source evidence, then runs the requested test once. Muse batches source/test reads, patches, runs the requested test, then runs it with -v to get individual test names; there is no source reread after verification.

For reference, the immediately preceding current-description thin-tail trials used Muse 7 calls/64,521 input and Qwen 4 calls/27,750 input. These are single, sequential samples with differing timestamps, outputs, cache/order conditions and uncontrolled cloud sampling. This is **not a new production speedup** or an attributable timing comparison: the applications already used hoisting before this evaluator change. No suppression of passing checks or additional model instructions is introduced.

## Validation and artifacts

All agent tests and go vet ./internal/agent pass, including the expanded HTTP fixture. Production files/binaries are unchanged from the fully tested dispatcher-description build installed immediately before this work.

Artifacts: .tmp/worker-production-profile-2026-09-27/{muse,qwen}/result.json, execution logs, wire.json, agent-tests.json and agent-vet.json.

Next evidence target remains a redundant or failed sequence captured under the actual production profile. A verbose rerun that adds test-case detail, by itself, is insufficient justification to block model-selected verification.
