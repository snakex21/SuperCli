# The 118-second compaction delay: native preparation evidence

The slow request spends **116.840 seconds between selection of an idle runtime slot and task launch**. Its backend-reported prompt evaluation takes only **939.07 ms for four tokens**. This rules out the earlier interpretation that the entire 118-second first-output delay was prompt evaluation. Cache/context preparation before launch is the strong explanation; the captured boundaries do not yet separate save, load and other preparation costs.

Deeper follow-up: [Cache allocation and paging](2026-10-10-cache-allocation-paging.md) recovers two real eviction announcements from the archive and adds one identical-body, event-driven ETW observation. The new run does not reproduce 116 seconds; it provides direct paging/copying evidence for its shorter preparation interval, preserving the historical attribution limits.

This diagnosis uses already-completed server logs and the existing client receipts. No new inference, warm-up, model load, cache clearing, slot pinning or changed request is performed. The user's model, thinking, effort, summary instruction and conversation history remain unchanged. Extracted telemetry retains allowlisted numbers, fixed phase names and source line numbers, without copying prompts, output or reasoning text from the native log.

## Correlated completed requests

| Observation | Original / task 45037 | Exact repeat / task 46405 | Previously rejected cue / task 47398 |
| --- | ---: | ---: | ---: |
| Client helper total | 157.469 s | 28.437 s | 142.425 s |
| Client first exposed model output | 118.183 s | 0.183 s | 84.175 s |
| Selected-slot to task launch | 116.839785 s | 0.000307 s | 73.262146 s |
| Backend prompt evaluation | 0.939070 s / 4 tokens | 0.156160 s / 4 tokens | 10.841670 s / 7990 tokens |
| Backend generation | 39.271950 s / 1367 tokens | 28.252740 s / 992 tokens | 58.289030 s / 1982 tokens |
| Native task launch to final timing record | 40.211146 s | 28.408915 s | 69.130738 s |
| Client first exposed output to completion | 39.286 s | 28.254 s | 58.251 s |
| Slot choice | Idle slot 0, LRU | LCP similarity 1, retained ratio 0.857 | Idle slot 0, LRU |

These are boundaries and intervals from different clocks, not independent additive cost buckets. Final native generation totals exactly match the corresponding client output counts: 1367, 992 and 1982. Together with task IDs and completion windows, they corroborate the request correlation; earlier generation-progress samples are not used as final totals. The existing cue remains rejected for loss of a continuing prohibition and slower generation; these new logs do not turn it into a speedup.

Both original requests have HTTP body SHA256 `e746786c4e5dd90f93761b927089a8afe071e9cd5d055ba39b22f2d46c12d0de`, effort `xhigh`, the same selected local model, one Complete and one POST. The cue has a different body and is not an identical-repeat sample. Client metadata discovery and HTTP connection/header times are small and cannot account for the long selected-slot interval.

For the original request, selection occurs at native clock 28983.298199 s and task launch at 29100.137984 s. Slot selection already identifies an idle slot (`task=-1`). The 116.839785-second interval therefore cannot be explained entirely by waiting for that selected slot to finish another active task. It begins after availability has been established. This does not prove that every earlier or unrelated request had no queue delay.

The exact repeat selects by longest common prefix at native 29395.482915 s and launches at 29395.483222 s. The contrast with LRU selection is strong evidence that the expensive path is preparation associated with switching/restoring a context, rather than summary wording or ordinary token decoding.

## What the runtime boundary means

Official upstream logs `processing task, is_child = ...` at the end of slot launch, after sampler setup. Before launch, `get_available_slot` can save the previous prompt state, load a cached prompt state, and update the prompt cache. LRU selection can enter this preparation branch; a sufficient LCP match can avoid it. The prompt-evaluation timer begins later, so it excludes that earlier preparation.

The source audit is pinned to upstream commit `69f201a2051ea9b9e9b50c3cc56afd9e2ae64414`. The installed LM Studio runtime's precise fork/revision has not been independently identified. Matching native grammars and observed lifecycle support this interpretation; upstream code alone does not measure the exact installed save/load sub-operation. [Pinned slot selection and cache preparation](https://github.com/ggml-org/llama.cpp/blob/69f201a2051ea9b9e9b50c3cc56afd9e2ae64414/tools/server/server-context.cpp#L1547-L1650), [launch boundary](https://github.com/ggml-org/llama.cpp/blob/69f201a2051ea9b9e9b50c3cc56afd9e2ae64414/tools/server/server-context.cpp#L1755-L1806).

The native logger's prefix contains an elapsed timestamp assigned before asynchronous printing. It uses system-clock elapsed time and is not a guaranteed monotonic clock; logging backpressure can occur before assignment. Archive headers have second precision and no timezone offsets. Local-time correlation, same-task numeric evidence and native differences support the result, but no unsupported sub-millisecond HTTP-admission or pure queue duration is fabricated. [Logger timestamp and queue](https://github.com/ggml-org/llama.cpp/blob/69f201a2051ea9b9e9b50c3cc56afd9e2ae64414/common/log.cpp#L219-L337).

Client cached-token zero is not evidence that caching was disabled. The native evaluated-token counts and the repeat's LCP match demonstrate why missing compatibility cache telemetry must remain unknown. A measured `cache_n` is not calculated as client input tokens minus native evaluated tokens without a verified same-task accounting contract. Counts and native timer definitions may include different framing or decode steps.

## Neutral improvements and rejected shortcuts

The ordinary default compactor already reuses the main provider, passes cancellation and purpose through, makes one helper call, and sends a stable local `cache_prompt:true` hint. No duplicate inference or random local cache key was found. That hint is not a documented guarantee of LM Studio's cache configuration. Changing the original main prompt into a separate summary instruction and plain-text history is intentional; carrying all old system instructions/tools into the summarizer would change precedence and cannot be presented as a neutral prefix-cache fix.

Request `cache_prompt=false` does not guard selection-time save/load/update in the pinned upstream source; it affects later prefix reuse. Disabling it could lose useful reuse while leaving the expensive preparation intact. No such speculative request or global cache change is tested or shipped. Forcing slot 0, adding another model instance or changing parallelism would also impose a backend/resource policy rather than being a demonstrated universal fix. [Prefix-cache option](https://github.com/ggml-org/llama.cpp/blob/69f201a2051ea9b9e9b50c3cc56afd9e2ae64414/tools/server/README.md#L537-L543).

A small independent shared-code defect is fixed: manual GUI compaction now attaches the validated saved session ID to the provider context, like normal conversation turns. Previously it fell back to the process identity. Existing transports can use the proper conversation identity for routing and prompt-cache grouping; other providers ignore it. The change does not alter the local model request body, history, reasoning effort, output limits, usage durability or cancellation. It is not credited with removing the measured local 116.84-second delay.

No client-only change proven to remove this backend preparation interval for all models is deployed. Exact save/load attribution and a backend-side fix require evidence from that runtime's preparation operations. The longer summary-generation instruction experiments remain rejected, preserving the existing agreement-handling contract.

## Validation and installation

The frozen manual GUI handler reproduces the missing session ID: all six invocations across success, failure and cancellation use the process fallback. A one-line candidate passes all cases. The test covers distinct saved sessions, one provider/meter call, parent values/deadline/cancellation, unchanged helper shape, durable archive, protected recent tail and per-session usage. Full WebGUI and LLM suites plus `go vet` pass after applying the patch. The preceding unchanged-source integration run already passed 19 Go targets and 309 UI tests; no additional UI-source change is made here.

Test-only bounded numeric SSE instrumentation is compiled and passes seven parser contracts plus the existing HTTP trace contract. It distinguishes missing cache fields from explicit zero and preserves exact Read bytes/errors/Close behavior with no readahead or extra request. It is not used to fabricate another inference once the completed log supplies the needed native timing evidence.

The native archive parser and prepared observer pass 13 synthetic contracts without a model request. The final extraction contains 98 numeric events and has SHA256 `B37A931DB054E58B0707B0B3ADA30C0B2A27EDF8D9A18B454983FAE68960884F`. The consolidated counters retain exact source-line provenance; queue wait, cache hit count and individual save/load/update durations remain null. An unrelated task in the alternative UTC correlation window is excluded from the three measured requests.

Both executables are built with stable before/after source hashes, atomically installed, and pass installed `--help` smoke. Previous binaries are preserved under `.tmp/summary-delay-deep-diagnosis/backups/bb27e05d-c901-48de-8124-e5b6bbd02c41/`.

| Executable | SHA256 |
| --- | --- |
| supercli.exe | 26447B5634032267FF0A978AF75E51B4341F7C8A424D922C71A5B9C6F90FE86D |
| supercli-web.exe | 6A6D2A6629159EC82A926D69A4E43146608350161F007F27FF80968E3D2DFD8C |

All new artifacts, caches, temporary files and backups are in the application folder. Native log access is read-only. No user/server setting, main summary prompt, GUI speed display, commit or push is changed. Restart a running GUI/CLI to use the session-context correction.

Evidence: `.tmp/summary-delay-deep-diagnosis/archive-audit-final/`, `native-counter-summary.json`, `README-observer.md`, `native-interpretation.md`, `cache-audit.md`, manual-session red/green receipts, `numeric-sse-contracts.log`, and the checks/build/install/smoke receipts. The independent interpretation report preserves its earlier 61-event audit scope; the final extraction adds the complete native generation counters reported above. The existing original helper receipts remain under `.tmp/summary-generation-profile/`.
