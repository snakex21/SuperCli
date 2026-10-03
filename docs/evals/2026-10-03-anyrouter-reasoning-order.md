# AnyRouter per-model API routing and native reasoning order

Baseline: 809258e187488e25e5ac4699f3052162359577e7. This tranche adds local per-model AnyRouter transport resolution and fixes GUI/TUI ordering for delayed native reasoning. No new agent prompt instructions, model probes at inference time or cross-model fallback are added.

## AnyRouter

The shared GUI/TUI/first-run preset is AnyRouter, https://anyrouter.top/v1, with a required API token. All 27 server-side UI catalogs carry its description. Recognized official HTTPS roots and full API endpoint URLs normalize to /v1. Lookalike hosts, custom proxy paths and unrelated providers retain their existing behavior.

Construction resolves the selected family: claude-* and *-cc-format use Anthropic Messages; gpt-5*, gpt-6*, codex-* and o1/o3/o4 use Responses; older gpt-*, chatgpt-* and gemini-* use Chat Completions. Unknown IDs keep the explicitly configured protocol. Existing saved openai, anthropic and responses provider rows all work through this resolver; both GUI factory and TUI/app construction use it. The post-configuration verification call resolves identically. A catalog listing is not proof that the upstream model is currently available.

The existing provider auto-detect selects a provider catalog dialect. It does not test every model. For AnyRouter the root is recognized locally, its OpenAI-shaped /models catalog remains discoverable, and inference selects the native dialect by model without another HTTP round trip. An older saved Anthropic row sends the catalog's required Bearer auth on its first discovery request. Its Messages path opts into the provider-required context-1m beta before the first request, removing a reproducible first HTTP 400. The 1M header advertises protocol compatibility; it does not insert 1M tokens or enlarge SuperCli's configured working context.

The OpenCode Zen request/serializer/header special route is unchanged. No saved key, active provider/model, context limit, LM Studio setting or user session data was rewritten.

## Authorized live results

Only synthetic short prompts used the user's saved key, read in memory. The official /v1/models catalog returned 17 IDs. The stale saved claude-fable-5-1 was absent; claude-fable-5-1-reversed was advertised. Neither catalog membership nor name similarity was used to silently replace a selected model.

- Anthropic Messages without the beta: HTTP 400 requiring 1M context. With the beta, attempted Claude models returned upstream 429/503 responses. A provider error for opus-5-5 explicitly cited unavailable Claude supply and suggested its GPT cc-format offering. That model also returned 429 during this test.
- gpt-6-astra through Chat Completions: HTTP 404 stating that API is unsupported for the selected model. A bare Responses-shaped payload returned invalid codex request. The existing full SuperCli standard Responses schema (typed message/content items and thread cache key, without ChatGPT-only impersonation headers) completed with OK in 7.1 s.
- Actual production factory + metering + public agent Loop, starting from the saved provider type anthropic: runtime *llm.ResponsesProvider, one call, reply OK, 7,728 ms, input 159, output 55, reasoning 48. Measured delivery rate 23.96 tokens/s. This is one short network observation, not a throughput benchmark.
- Tool continuation attempt: HTTP 500 get_channel_failed with an upstream load-limit message, zero tools executed. One later bounded test returned the provider's explicit eastus2 token-rate-limit stream error, again before executing a tool. Therefore live tool continuation remains unconfirmed while this provider is rate/capacity limited. No repeated availability polling or model replacement was added.
- Some advertised older models also rejected an attempted API. The resolver supplies the selected family's normal dialect; it cannot restore upstream supply or account capacity.

Local .tmp/goal-anyrouter-2026-10-03 holds redacted statuses, synthetic request fixtures and production-loop reports. API tokens are absent from evidence and source.

## Presentation and honest generation speed

A usage-only reasoning count can arrive after the visible answer. The previous GUI helper appended its placeholder after the answer; native late thought fragments had the same ordering problem in GUI and TUI. Native thoughts now occupy one leading block, including fragments received after prose. Unchanged answer DOM and folded state are retained. Inline/legacy parsing and tool boundaries retain their existing semantics; saved transcript bytes are not rewritten.

The reported 30,314.9 tokens/s had a separate cause: usage counted hidden reasoning, while its clock began only at the almost-complete visible answer. Native Responses reasoning-item-added now supplies timing metadata, without inventing or showing hidden thought text. Zen's special stream route is excluded from this new marker. Usage-less calls, single output batches, and hidden reasoning with no observed start yield no invented rate. If one successful call has an unmeasurable interval, the whole turn omits the partial rate. Token totals remain exact provider usage.

No new per-token timer, tokenizer, model call or animation delay is added. GUI order correction is DOM/state correctness evidence, not a native FPS/RSS improvement claim.

## Validation

Focused Go suites passed for agent, llm and subpackages, provider factory, app, TUI and UI language catalogs. Full go test ./... passed in 71 tested packages; go vet passed. All 194 Node UI tests passed, including the new production-dispatch ordering cases. Gofmt and git diff --check passed. Before/after SHA-256 snapshots showed the same 1,864 source/test/module files during those checks.

Regression coverage includes endpoint origins, all saved protocol types and three API families, first-request Messages headers, passive provider detection, Responses reasoning timing with hidden-text and Zen controls, public Loop rates for visible/hidden/batched/delayed streams, actual assistant sibling order, retained answer nodes, folded state, done/EOF/error closure, stale boundary recovery, TUI copy/reset ownership and tool-segment separation.

All ten CGO-disabled builds passed: CLI and GUI for Windows amd64, Linux amd64/arm64 and macOS amd64/arm64. Their SHA-256 manifests are saved locally; build-time source snapshots matched the checked sources. Both local Windows EXEs were replaced with 1.0.4-dev.21 after backing up and verifying the exact previous/current hashes. No running application was terminated or restarted. CLI --version/--help and GUI Go build metadata checks passed; native GUI layout and cross-platform execution were not measured. Live AnyRouter capacity remains an external limitation.
