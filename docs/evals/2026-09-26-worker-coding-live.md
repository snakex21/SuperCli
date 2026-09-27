# Live code-worker schema comparison — 2026-09-26

## Scenario

An opt-in production AgentTool/Loop replay creates a synthetic Go project containing cache, config and transport packages. The worker receives a Polish bug report: an expired cache entry is returned exactly at its expiry timestamp. It must find the code, fix the boundary and run existing tests, then return its report. Test files must remain unchanged. A separate go test ./... runs before and after every trial: first it must fail at now=100, then pass after the edit.

Only synthetic files reach the model. File tools are scoped to that workspace. ctx_execute uses the real command runner with a test-only allowlist for Go tests and targeted gofmt. This wrapper does not affect production. The schemas include the real six document/archive tools to reproduce the native worker's former eager schema cost. process_session is absent in both arms, isolating schema deferral from the new process capability.

The eager arm clears only the built-in code role's DeferredTools; the deferred arm uses the new role unchanged. All other production improvements remain enabled in both. This is not a comparison against the old published binary.

Models:
- local qwen3.8-27b-uncensored, OpenAI-compatible endpoint, temperature=0, fixed seed;
- free muse-spark-1.2-contributor-free via the existing Zen Responses path.

Both request low reasoning. There are two native pairs per model, with reversed arm order in the second pair. Different models run concurrently, but each model's arms run sequentially. Timing covers the worker execution, excluding fixture setup and independent verification.

## Native results

All eight native runs completed, passed the independent tests, retained the original test source and made zero rejected command requests.

| Model / pair | Eager calls | Deferred calls | Eager input tokens | Deferred input tokens | Eager time | Deferred time |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Qwen / first | 5 | 5 | 29,538 | 15,538 | 31.263 s | 25.910 s |
| Qwen / reversed | 5 | 5 | 29,564 | 15,486 | 23.740 s | 24.228 s |
| Muse / first | 6 | 5 | 36,948 | 16,559 | 14.516 s | 9.399 s |
| Muse / reversed | 6 | 5 | 37,459 | 16,770 | 11.876 s | 9.225 s |

Serialized tool definitions were 19,090 bytes eager versus 6,811 deferred in this fixture, a 12,279-byte difference on every request.

Mean input reduction: Qwen about 47.5%, Muse about 55.2%. Qwen used the same six tool calls and five model requests in both pairs. Muse eager used an additional read_many round; deferred used one fewer model request in both pairs. This is observed behavior on a small fixture, not a guarantee for other work.

Qwen wall time is mixed: the reversed deferred run was 0.488 seconds slower. The two-sample means are 27.502 → 25.069 seconds for Qwen and 13.196 → 9.312 seconds for Muse. Small samples, provider variation, cache state and differing tool choices prevent a general latency or billing claim.

## Thin-protocol probes and discovered defect

A current-code thin probe on Qwen completed in five calls and passed. Muse's first thin probe hit the evaluation-only eight-step cap after fixing the code and running a successful test, without producing its final report. Its trace exposed a real error: read_lines with only a filename passed schema validation but failed with to=0.

[The bounded-default read fix](2026-09-26-read-default-range.md) addresses that specific defect. The first post-fix Muse trace successfully read a file without a range but also requested go test ./... -v, which the test wrapper incorrectly rejected. That run is confounded and not counted as a speedup. The wrapper now accepts that equivalent flag order.

The final thin Muse probe completed in seven calls, passed independently, kept tests unchanged and made zero rejected requests. It also exercised the repaired file-only read. These thin traces still contain repeated/extra reads; they do not prove the whole exploration behavior is optimized, and there is no matched thin A/B latency claim.

## Reproduction and artifacts

Opt-in test: internal/agent/worker_coding_live_test.go, TestWorkerCodingSchemasAB_Live. It is skipped unless SUPERCLI_CODING_URL, SUPERCLI_CODING_MODEL and an absolute SUPERCLI_CODING_OUT directory are set. SUPERCLI_CODING_ORDER chooses eager,deferred or reversed order; SUPERCLI_CODING_ROUTE=thin selects the compact protocol. Use a new output directory for each run; existing workspaces are not overwritten.

Artifacts: .tmp/worker-coding-2026-09-26/summary.json and per-arm JSON files under qwen-native, qwen-native-reverse, muse-native, muse-native-reverse, qwen-thin, muse-thin, muse-thin-read-fix and muse-thin-read-fix-gate. Failure traces are retained as well as passing ones.

No special Zen transport, application instructions or automatic production model calls were changed for this experiment.
