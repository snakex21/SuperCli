# Requested tool contracts preserve the coordinator prefix

Date: 2026-10-02. Scope: request preparation and local Qwen prompt evaluation; no GUI frame-rate or total application RSS measurement.

## Observed problem

The requested screenshot/process capability was promoted into the wire `tools` list for a screenshot turn, then removed on the next unrelated turn. In stable thin-tool mode this changed the schemas rendered near the beginning of the model prompt, despite an unchanged leading system/catalog. A bounded review of completed LM Studio requests showed 16 definitions (13,885 wire bytes) falling back to 14 (10,498 bytes), followed by full prompt evaluation. The added process-session contract was about 542 locally estimated tokens; it did not explain the entire existing 6–7k input.

For four completed user requests the server reported 7,152/2,029/6,160/2,946 newly evaluated tokens, with 26.335/7.924/22.971/11.048 seconds of prompt evaluation. Full input was 7,152/7,316/6,160/7,721 tokens. The difference matters: missing cached-token usage is unknown cache coverage, rather than proof of zero reuse. These server timings closely matched time to first model output, so the observed long wait was primarily model prefill. No background helper call was present in the examined screenshot turns. This is distinct from host preparation costs around a millisecond and the separately corrected desktop-versus-screen capture behavior.

## Controlled live comparison

Used the unchanged OpenAI-compatible provider implementation with `qwen3.8-27b-uncensored` on LM Studio, one serial request at a time, maximum 256 output tokens, temperature 0, seed 0, automatic prompt caching and default reasoning control. There were exactly four wire requests, no retries, model reloads, KV resets or user configuration changes. Each arm first warmed the same full synthetic GUI coordinator prefix: 14 core contracts, the normal catalog and system context. A subsequent controlled request asked for desktop wallpaper/icons via `source=desktop`, `attach=false`. No suggested command or capture was executed and no image pixels or private project history were sent.

The legacy arm advertised 16 native contracts on the screenshot request. The candidate retained the same 14 native contracts and provided the exact full descriptions and schemas of the requested tools in the ephemeral trailing context, callable through the existing `invoke_tool`.

| Completed request | Total input | Newly evaluated tokens | Prompt evaluation | First model output | Client total | Output / reasoning | Finish |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| Legacy warm-up | 4,348 | 2,300 | 8.373 s | 8.427 s | 11.632 s | 91 / 86 | stop |
| Legacy screenshot | 5,364 | 5,364 | 18.766 s | 18.816 s | 21.908 s | 86 / 44 | tool_calls |
| Candidate warm-up | 4,348 | 4,348 | 15.372 s | 15.408 s | 18.468 s | 91 / 86 | stop |
| Candidate screenshot | 5,343 | 1,106 | 4.189 s | 4.234 s | 7.261 s | 83 / 31 | tool_calls |

Both screenshot requests selected the correct desktop source and `attach=false`; legacy returned native `send_screenshot`, candidate returned `invoke_tool` targeting `send_screenshot`. The candidate evaluated 79.4% fewer tokens and reached first output 77.5% sooner in this pair. Total input decreased by only 21 tokens (0.4%). This measures prefix reuse, not a comparable reduction in total context or billing.

Both warm-up bodies were exactly 16,022 bytes with SHA-256 `fe43643cb408eaaa4dac66f9f0cde85789fd2fab4b415d8bebdf0ea68185cad5`. The base core-definition hash was `b46303d7da3277d04881393d6cb6ebc914a947e7f868cab10001d56f139085d5`. The leading-system hash stayed `5c2b00480e9cf6bdd333ede0fe6f1d845fa3ca60e8d15f79f4959ffc01528970`. Legacy screenshot wire body was 19,750 bytes; candidate was 20,895 bytes because contracts use JSON-escaped schema strings plus a short dispatcher instruction. The rough local estimate increased from 5,796 to 5,960 tokens while provider input decreased slightly; the estimator is not the model tokenizer.

This is one ordered A/B pair on one model and device. The first legacy warm-up reused older slot data, whereas the candidate warm-up reevaluated its prefix after the preceding schema change. Both screenshot arms immediately followed their own identical 14-tool warm-up, but cache state, ordering and hardware variability prevent a general timing guarantee. The fixture uses a fixed synthetic assistant reply between requests; it is not an end-to-end live agent or signed-history test. Cold requests, chat-to-coordinator route changes and other providers may not gain cache reuse. Later descriptor shortening is a separate change and was not included in these live numbers. No RAM or CPU allocation improvement is claimed.

## Production change and guards

The common path uses the requested-contract tail only for coordinator + thin tools + stable toolset when `invoke_tool` is registered, visible and actually schema-carrying. Embedders without that dispatcher, orchestrator mode and dynamic/full-schema modes retain the native promotion path. Final-reply-only requests expose neither requested contracts nor tools. No provider or special Zen transport code is changed.

Every request reads the current registered description and raw schema, including replacement and late registration. Screenshot, process-session and headless contracts remain available throughout all steps of the requested run and disappear on the next unrelated run. Their removal does not change the stable core definitions. The tail is priced in `contextTail`/exact prepared-request accounting and never enters persisted messages, UI history, discovery state, activation state or native/signed reasoning. Existing target resolution, argument validation, verification, approvals and cancellation remain the execution path. No discovery model round is added.

## Deterministic verification

New tests cover exact Unicode/schema preservation, stable wire definitions, late registration/replacement, hidden/absent/restricted dispatcher, orchestrator and thin/stable fallback, light routes, final-only requests, native/parts history immutability, next-run reset and target validation. Public `Loop.Run` fixtures execute mock desktop capture in one tool step and mock owned-app start then screenshot in two tool steps, with correct IDs/canonical target attribution and stable core/system throughout. No application or screen capture runs in these fixtures. Existing invoke and cancellation tests exercise the unchanged normal execution guards.

Passed: `go test ./internal/agent -run "RequestedTool|Screenshot|Headless|Native|Reasoning|Prefix|Invoke|Cancel|PreparedRequest" -count=1` and `go vet ./internal/agent`. The prepared-request estimate equals the exact assembled request and requested-tail growth is counted once. The pre-existing standalone window estimate merges hoisted leading systems differently and is 15 tokens higher in the small fixture; this unrelated conservative difference was not changed. Full integration checks and executable builds are owned by the parent task.

## Reproduction artifacts

Local controlled artifacts are under `.tmp/goal-runtime-round6-2026-10-02`: `live.go`, `wire_helper.go`, `live-overlay.json`, `run-live.cmd`, `live-results.redacted.json`, `live-timings.redacted.txt`, and `run-port-tests.cmd`. Replay fixtures containing raw user data remain private and are not included in this report. The redacted live output contains only timings, usage, body hashes and selected argument shape.
