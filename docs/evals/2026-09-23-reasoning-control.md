# Reasoning control verification — 2026-09-23

The reasoning dial now reaches the next request made by an existing provider.
This is a control-path correction, not a claim that every backend implements
all reasoning levels or that higher/lower settings guarantee a given latency.

## Findings and corrections

- Standard Responses requests for models outside the old name heuristic could
  lose the selected effort; catalog reasoning support then inserted a fixed
  `medium`. Request building now uses the current preference and endpoint/model
  evidence. Provider default leaves effort unset. Explicit `none` is preserved
  for standard Responses so a backend can accept it or return its supported list.
- GPT-6 names now use the same reasoning control family as GPT-5.
- Local Chat Completions used the nested gateway `reasoning` object for unknown
  families such as Qwen. It now sends the standard `reasoning_effort` field.
  OpenCode gateway wrappers retain their previous format.
- Responses rejection learning is scoped to the endpoint/model. An adjusted or
  rejected value remains adjusted/omitted on later turns and does not affect
  another provider with the same model. Image fallback retains the learned value.
- Native LM Studio discovery now accepts string-valued `architecture`, as well
  as router-style objects, and works for loopback IPs as well as localhost.
  Its on/off-only metadata reaches the shared GUI/TUI reasoning state.
- The GUI and TUI describe a toggle-only model as on/off, show backend adjustments,
  and distinguish the saved preference from a control that is not sent.

The special OpenCode Zen serializer, headers, session identifiers, tool aliases,
and transport were preserved. Mock HTTP tests confirm that low/high/xhigh still
reach that path with its original request dialect. No live Zen inference was
performed; wire acceptance cannot establish whether Zen distinguishes the levels.

## Local experiment

LM Studio was already running at `http://127.0.0.1:1234/v1`, with
`qwen3.8-27b-uncensored`, Q4_K_M and 100,608 loaded context tokens.
No model/server settings were changed. The native model metadata advertised
`allowed_options: ["off", "on"]`.

The prompt was “Reply with just OK.” No tools were offered and no repository
files were included. The initial direct-HTTP comparison used temperature 0,
seed 42 and a 24-token output cap:

| Request parameter | Reasoning tokens | Answer |
|---|---:|---|
| old nested `reasoning: {effort: "none"}` | 13 | OK |
| corrected `reasoning_effort: "none"` | 0 | OK |
| flat `reasoning_effort: "low"` | 13 | OK |
| flat `reasoning_effort: "high"` | 13 | OK |

A separate final check used the actual changed SuperCLI provider, kept the same
provider instance across changes, and capped output at 32 tokens:

| Selected | Metadata interpretation | Reasoning tokens | Output tokens | Elapsed |
|---|---|---:|---:|---:|
| low | on | 19 | 23 | 926 ms |
| none | off | 0 | 2 | 335 ms |
| high | on | 14 | 18 | 811 ms |

All answers were OK. These are short functional checks, not a speed benchmark.
Cache state, sampling, and request ordering affect timings. The variation between
low and high does not prove graded effort support; this model advertises only on/off.

Reports: `.tmp/reasoning-local-probe.json`, `.tmp/reasoning-local-after.json`.
The opt-in `TestReasoningLMStudioLive` requires a local URL, explicit model, and
report path. It is skipped in ordinary tests.

## Validation

- Request-level low → high → xhigh → none → default tests, without provider rebuilds.
- GUI POST and TUI menu selection → actual HTTP request tests.
- Adjustment/unsupported retries, subsequent turns, and endpoint isolation.
- Native metadata parsing and merging, including loopback discovery.
- Zen request-shape and header regressions.
- Full `go test ./...` and `go vet ./...`; JavaScript syntax checks and `git diff --check`.

No instructions were added to model prompts and no extra model calls are needed
to apply the dial. Local capability information comes from the existing model
discovery operation.

Protocol references:
[OpenAI reasoning](https://developers.openai.com/api/docs/guides/reasoning),
[llama.cpp Chat Completions parameters](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md),
[LM Studio Responses](https://lmstudio.ai/docs/developer/openai-compat/responses).
