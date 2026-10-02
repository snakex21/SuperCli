# Lossless tool-contract projection and Qwen prefill diagnosis — 2026-10-02

## Scope

This round tests production-shaped GUI coordinator requests through the unchanged OpenAI-compatible provider with the already loaded local model, qwen3.8-27b-uncensored. The desktop fixtures contain the actual current core definitions, hoisted catalog and requested screenshot/process contracts; they are not a minimal media-only toolset. No screenshots, pixels, real commands, application launches or configuration/KV-cache resets occur. Tools are validated against an isolated registry with mock callbacks only. Four requests run serially, without retries, and each produces exactly one wire call.

The implementation is common to stable thin-tool coordinator requests. The registered tool Specs, validation, execution permissions, discovery, provider protocols and the special Zen path remain unchanged. Full/dynamic/orchestrator/light-route profiles keep their existing definition behavior. This does not prove a GUI FPS, RSS or population-wide latency gain.

Private inputs and redacted measurement artifacts are under .tmp/goal-runtime-round7-2026-10-02. No raw user prompt, reasoning text, session identifier or credentials enter this report.

## What is actually sent

The isolated full-GUI registry fixture has 45 registered tools, 25 visible tools and 14 native coordinator definitions. A greeting carries three definitions. Preflight therefore does not indiscriminately send all registered tools. The coordinator native schemas and catalog still contribute substantially more input than the roughly 294-token repo preflight block alone.

The hoisted catalog already excludes full native core definitions. The built-in invoke_tool Eligible description separately repeats four signatures whose exact full schemas are also in the request: list_dir, read_lines, read_many and search_code. Other dormant/direct eligible signatures remain useful and are retained.

## Accepted change

Requested-tool tail contracts previously quoted the whole Schema JSON as a string. Valid JSON object schemas now use json.RawMessage, removing escaping without interpreting numbers or weakening any constraints. Numeric literals, duplicate keys, enum/default text and Unicode are retained. If any contract has an empty, malformed or non-object schema, the entire tail uses its exact legacy string rendering.

Only the built-in invoke_tool wire description in thin+stable coordinator mode omits signatures duplicated by actual full native definitions. Recognition requires the built-in schema/base description and every advertised signature to match the current registered read-only contract. Custom instructions, schemas or outdated/unknown signatures leave the description unchanged. Projection never mutates a registered Spec, and an unrelated late registration does not regenerate its frozen catalog. Recognition uses the canonical empty-registry dispatcher descriptor so it does not rebuild an unused full Eligible catalog. Existing definition-snapshot invalidation still follows registry/route/profile changes; no new cache or state table is introduced.

These changes preserve the stable native prefix across screenshot/headless flags and normal steps. Requested schemas remain transient tail context, do not enter UI/user history, and are priced once by the same request preparation path.

## Controlled desktop comparison

Both arms used all 14 native core definitions, the complete coordinator catalog, the same desktop task, max output 256, temperature 0 and seed 0. Similar distinct first-system nonces forced comparable cold requests; LM Studio confirms that every input token was evaluated in both arms. All schema facts were preserved. The candidate combines object-valued requested schemas and the four redundant Eligible omissions.

| Metric | Current | Candidate |
|---|---:|---:|
| Actual request bytes | 20,122 | 19,245 |
| Actual input tokens | 5,187 | 5,026 |
| Native evaluated prompt tokens | 5,187 | 5,026 |
| Native prompt evaluation | 18,212.07 ms | 18,082.65 ms |
| Client first model output | 18,597 ms | 18,234 ms |
| Client total | 22,727 ms | 22,440 ms |
| Output tokens | 117 | 119 |
| Native decoder speed | 28.10 tok/s | 28.10 tok/s |
| Finish reason | tool_calls | tool_calls |

Both arms selected invoke_tool targeting send_screenshot with source=desktop and attach=false, and passed the original target schema through mock-only Registry.Execute. No extra discovery/model turn was added. Input fell by 161 actual tokens (3.10%) and wire size by 877 bytes (4.36%). The native prefill difference was only 129.42 ms (0.71%) in this single ordered pair; it is too small and noisy to promise a material latency percentage. The robust finding is fewer tokens with the correct tested call.

No warm A/B claim is made in this round. The earlier stable requested-contract placement measurement remains separate.

## Greeting slowdown is real, but not caused by added screenshot pixels

An exact archived greeting request from 14:29:55 was replayed with identical messages, three tool definitions and parameters, except the explicit 256-token output cap. Both original and replay report 1,683 input tokens and zero image parts. Actual decoded request parity was checked after completion; only max_tokens differs.

| Native metric | Original finished 14:30:00 | Replay finished 22:16:04 |
|---|---:|---:|
| Evaluated prompt tokens | 1,683 | 1,032 |
| Native prompt evaluation | 1,926.82 ms | 3,718.93 ms |
| Native decoder speed | 36.19 tok/s | 28.05 tok/s |
| Output tokens | 79 | 84 |

The replay reused part of the prompt, so total prefill times are not a cold/cold latency comparison. Its native decoder is nevertheless about 22.5% slower than the earlier completed request, with the same content and tool contracts and no capture/image execution. Prior completed three-tool greetings and the previous synthetic no-pixel tool-selection run also show the lower native rate. This isolates an observable LM Studio/backend-state slowdown from a claim that screenshot schema changes or GUI rendering directly reduced native decoder speed. The exact cause is not established here; no settings are changed to guess at it.

The second greeting replay finished successfully with three tools, zero tool calls and 51 output tokens. It reused almost all prompt cache (only four newly evaluated tokens), and omitted the original reasoning_effort=max field; it is reported as a smoke check, not used for causal timing comparison. CachedInput=0 in compatible usage metadata does not prove an empty cache; the native evaluated-token records are decisive here.

## Additional backend evidence

Completed model-load records show the last load finished at 12:09:58, with no new initialization between the faster 14:30 and slower 16:58 requests. The native elapsed counter and reused-graph counter continue across this interval. Read-only configuration snapshots retain the ROCm AVX2 engine 2.50.0, Q4_K_M weights, symmetric q4_0 K/V cache, flash attention and roughly 100k context. These records contain no GPU clocks, temperature, residency or exact offload telemetry, so they do not establish why throughput changed. Cache evictions also occurred before the slowdown and are not causal proof.

Upstream reports describe [similar mrope image position warnings in hybrid Qwen models](https://github.com/ggml-org/llama.cpp/issues/28166) and [ROCm prefill dispatch overhead for Qwen 3.5 27B](https://github.com/ggml-org/llama.cpp/issues/20292). They concern different platforms/builds and are investigation leads, not confirmation of this Windows regression. No engine, model, quantization, context or cache setting was changed. Private redacted load evidence is under .tmp/goal-lmstudio-load-round7-2026-10-02.

## Rejected alternatives

A proposed ask_user $defs/$ref rewrite saved 176 schema bytes, but the local custom registry validator does not resolve these references. An isolated overlay proved that three formerly rejected option structures became accepted: too few options, a numeric label and a missing label. It was not ported. A provider-only reference projection would need separate parser/quality evidence and is not justified by this small saving now.

A smaller always-on native core was not adopted: deferred ask_user/read_image contracts can add discovery turns and change mixed coding/question/vision behavior. The accepted projection removes duplicated representation, not capabilities or instructions needed to execute a task.

## Validation and limits

Focused requested/invoke/route/cache/catalog/screenshot/headless agent tests passed, including public Run desktop and owned-app start/screenshot workflows. go vet ./internal/agent passed. New tests cover raw numeric/schema literals, Unicode and enum text, exact legacy fallback, dormant signatures, custom dispatchers, full/dynamic/orchestrator/light profiles, provider mutation isolation, late registration and replacement, final-only behavior and prefix stability. Existing tests retain native/multimodal history identity, request token accounting and ordinary validation/refusal paths.

This is four controlled requests on one loaded local Qwen session, with one desktop call per arm and mock execution. It establishes bounded payload and tested semantic behavior; it is not a broad provider benchmark or proof of accelerated real screenshot encoding, GUI rendering, overall agent completion or RAM use. Zen/Kilo were not called in this round.
