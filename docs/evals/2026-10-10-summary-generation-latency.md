# Summary generation latency — 2026-10-10

The dominant delay in the slow measured summary occurs after the server has accepted the HTTP request and before its first exposed model output. The same unchanged request later starts almost immediately. This is not a measured improvement from a production prompt change. A tested prompt cue increases generation work and drops a continuing constraint, so it is rejected. The shipped change fixes a separate, protocol-level wait after Anthropic has already completed its answer.

Follow-up native-log diagnosis: [The 118-second compaction delay](2026-10-10-compaction-cache-preparation.md) correlates the existing requests with completed backend tasks. It locates 116.840 seconds after idle-slot selection and before launch, while backend prompt evaluation takes 939.07 ms. The original client-only measurements below remain unchanged.

## Controlled helper-only diagnosis

The opt-in runtime harness invokes the production `SummarizeForCompaction` once, without tools or a following main-model turn. It reads the actual portable global and workspace configuration. The workspace overrides global effort `high` with `xhigh`; thinking remains enabled. Every recorded HTTP request retains `reasoning_effort: xhigh`, the selected model and `cache_prompt: true`. Configured maximum tokens is zero, so no lower output limit is inserted. A diagnostic transport permits one POST and blocks any compatibility retry before transmission; no retry or fallback is used in the completed runs.

Model used for live diagnosis: `qwen3.8-27b-uncensored` through LM Studio's OpenAI-compatible endpoint. This is one available local model, not evidence of inference speed across all models. The instrumentation and terminal handling are shared code, without model-name or website detection.

The fixed input contains thirteen older messages from a completed Node coding task: failing tests, a pricing fix, passing tests and still-applicable user constraints. It also contains 96 numbered synthetic maintenance notes in a long user brief. The helper's projected transcript is 25,996 bytes; the original instruction is 870 bytes. Historical commands are data and are not executed again. This is a reproducible diagnosis fixture, not an unmodified natural conversation or an overall GUI-turn benchmark.

Source coding receipt SHA256: `384a4836378f5ac1a147da2e6668c38f3c6eca265b3f355f05538a887df04670`. The two unchanged runs have identical HTTP body SHA256 `e746786c4e5dd90f93761b927089a8afe071e9cd5d055ba39b22f2d46c12d0de`.

| Run | Helper total | Before first exposed output | First output to completion | Input / output / reasoning tokens | Continuing constraints |
| --- | ---: | ---: | ---: | --- | --- |
| Original A1 | 157.469 s | 118.183 s | 39.286 s | 8180 / 1367 / 942 | Preserved in manual review |
| Exact original repeat A2 | 28.437 s | 0.183 s | 28.254 s | 8180 / 992 / 531 | Preserved in manual review |
| Test-only cue B1 | 142.425 s | 84.175 s | 58.251 s | 8210 / 1982 / 1632 | Protected test/catalog rule not preserved as a future prohibition |

Output tokens include reasoning tokens; those figures are not added together. After the first exposed reasoning, A1 spends 27.173 seconds before visible text and 12.113 seconds from visible text to completion. Client connection and request writing take about 1.06 ms; response headers arrive after 78.08 ms. Metadata discovery before inference takes 106 ms. These observations rule out client request preparation or an ordinary network-connect delay as the 118-second bottleneck in this run.

TTFT is an observed client boundary, not an exact backend prompt-evaluation timer. Server queueing, prompt evaluation, cache/load scheduling, buffering or unexposed computation cannot be separated from these timestamps alone. `CallStat.PrefillTokensPerSecond` is a derived estimate, not backend telemetry. Zero-filled cached-token accounting cannot distinguish unavailable cache data from a reported zero. The exact-repeat improvement is compatible with prefix reuse or other backend-state changes, but does not prove the cause or a universal speedup.

Raw receipts, instrumentation contracts and sequential logs are under `.tmp/summary-generation-profile/`. The harness adds HTTP trace timestamps without changing request bytes and checks that a second HTTP attempt is blocked. No thinking text is saved in the new timing telemetry; only its byte count and observed boundaries are retained.

### Available server telemetry

A single read-only endpoint inventory finds one loaded model with configured context 100,608. It provides no historical prompt-evaluation, queue, load or cache duration. `/metrics` and `/props` return JSON errors despite status 200, so neither is a usable metrics endpoint here. LM Studio's [API comparison](https://lmstudio.ai/docs/developer/rest) documents load and prompt-processing events for its native chat endpoint, whereas the current OpenAI-compatible interface does not expose them. Switching protocol would alter the inference request and cannot be presented as a neutral comparison.

The installed CLI supports a runtime push log stream, but its banner precedes creation of the subscription and there is no exposed subscription-ready acknowledgement. Waiting for the first runtime log might depend on model activity. No observer or extra model call is started merely to manufacture readiness. No raw runtime log, prompt or thinking output is stored, and no polling or model/server setting change is introduced. See `.tmp/summary-generation-profile/runtime-observer/readiness-audit.md` and `.tmp/summary-wiring-audit/native-telemetry-diagnosis.md` for the endpoint inventory and primary source audit. The pre-output cause therefore remains unclassified rather than being guessed as cache or prompt processing.

## Rejected cue

The candidate leaves the original system instruction and every transcript byte intact. It adds a 167-byte framing cue around the user transcript, marking historical messages and asking for the continuation summary. It still uses two messages, nil tools, one Complete, the same thinking and output settings. Both baseline and candidate request-preservation contracts pass with synthetic outputs; those contracts do not establish real summary quality.

Real B1 generation after first output takes 58.251 seconds, exceeding both unchanged observations, and reports 1,632 reasoning tokens. Its lower total compared with A1 comes from a shorter pre-output wait, not faster generation. Manual review finds tests and catalog mentioned only as previously unchanged files. The summary's future constraints no longer prohibit modifying them. This is sufficient to reject the candidate without another inference: both quality and generation work are worse in the observed sample. It remains a test-only overlay under `.tmp/summary-generation-audit/`; production `context_summarize.go` is unchanged.

No lowering of reasoning effort, disabling thinking, switching models, cutting the history or output limit, or deleting user agreements is deployed. Prompt changes already rejected in the preceding compaction-latency report are not repeated.

## Completed-answer transport fix

Anthropic's final `message_stop` event follows content blocks, reasoning signatures and cumulative usage. The old parser emitted that finished result but then continued reading until the HTTP body reached EOF. A proxy or server leaving the body open could delay completion even though the full answer had arrived.

`internal/llm/anthropic_stream.go` now exits on a private terminal sentinel immediately after successfully emitting final usage and finish reason. Earlier errors, missing terminal events and cancellation remain errors. Text, tool calls, signed native reasoning blocks, unknown native fields and final token accounting remain unchanged. Generic SSE parsing is unchanged. This applies to every model using this provider protocol, including summary and normal calls; it does not accelerate the local Qwen's measured pre-output delay or change model generation.

A gated-reader reproducer fails before the patch and passes afterward, with no sleeps or manufactured live delays. Focused correctness passes eleven top-level tests, including actual Complete → mock HTTP → Metered, one request, exact output order and usage, fragmented EOF, errors before completion, cancellation and truncated finish handling. OpenAI `[DONE]` and Responses `response.completed` controls pass unchanged. OpenAI chat is deliberately not stopped at `finish_reason`, because a subsequent usage frame may still be required.

Protocol reference: [Anthropic streaming documentation](https://platform.claude.com/docs/en/build-with-claude/streaming). Reproducer, focused logs, receipt and source hashes are in `.tmp/summary-transport-audit/`.

## Final validation

Combined validation passes on the first run: Go tests for all 19 targets (the CLI command has no test files), 309/309 UI tests with no skips/failures, and `go vet` for all 19 targets. The new HTTP instrumentation contract runs in the normal agent suite; the actual-model runtime diagnostic remains opt-in. Focused Anthropic correctness is covered again by the full LLM suite. No test repair or production prompt change is needed.

Both executables are built with matching source hashes before/after compilation and atomically installed. Previous binaries are preserved with verified hashes under `.tmp/summary-generation-profile/backups/5fc56093-b915-4b89-b969-50ddb1b61690/`. Installed `--help` smoke passes for both. Checks, source manifest, build, installation and smoke receipts are under `.tmp/summary-generation-profile/`.

| Installed executable | SHA256 |
| --- | --- |
| supercli.exe | 08AF7F875DD6EDD29421E85D54EC036BCB9DCC5DC728345792C8A6A443A524AB |
| supercli-web.exe | 4CD1ED35B8A56A27A64CEEA57B0782BCB35568B72B28C2D8A499889017D7CCFE |

All artifacts, caches, temporary files and backups remain in the application folder. No user configuration or GUI generation-speed display is modified, and no commit or push is made. Restart a running CLI/GUI to load the updated executable. The observed inference variability does not justify a claim of universally faster summary generation; only the completed-answer wait has a demonstrated neutral production fix in this round.
