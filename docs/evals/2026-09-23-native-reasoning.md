# Native reasoning history — 2026-09-23

This supersedes the blanket-removal recommendation in the earlier reasoning-history report.

## Decision

Keep completed-turn reasoning in its provider-native form, separately from display text.
Do not append a second copy as a system instruction. The shared agent loop persists
native state for GUI, TUI and resumed workers. Model/endpoint/protocol switches filter
incompatible state from the request, without changing the archive. Normal context
budgets and compaction still bound history; native state is included in estimates.

This is a policy supported by a small controlled sample, not a promise that keeping
reasoning always wins. A larger input can reduce regenerated reasoning and extra
tool calls. Fewer input tokens alone do not establish a faster or cheaper task.

## Paired live experiment

Two synthetic JavaScript repairs: escaped comma fields (9 assertions) and TTL cache
semantics (7 assertions). The first turn reads a fixture and explains a plan. After
that turn completes, clone the exact same native conversation. KEEP replays prior
reasoning; DROP removes only reasoning from completed prior turns. Both arms retain
reasoning within any new tool loop and receive the same visible plan/specification.
No user project files or shell commands are available. Generated code runs against
fixed assertions in an isolated VM with code generation disabled and a time limit.

Measured wall times below cover the implementation turn, including any repeated tool
round trips. Shared planning time is excluded. Input usage is accumulated over all
requests, rather than equated with context size.

| Model / fixture / run | KEEP seconds | DROP seconds | KEEP calls | DROP calls |
| --- | ---: | ---: | ---: | ---: |
| Qwen / parser / pilot | 42.006 | 42.471 | 1 | 2 |
| Qwen / cache / pilot | 16.432 | 40.403 | 1 | 2 |
| Qwen / parser / repeat | 26.845 | 46.376 | 1 | 1 |
| Qwen / cache / repeat | 20.323 | 5.979 | 1 | 1 |
| MiMo / parser / pilot | 2.609 | 2.495 | 1 | 1 |
| MiMo / parser / repeat | 5.096 | 12.713 | 1 | 1 |
| MiMo / cache / repeat | 2.570 | 4.678 | 1 | 1 |
| Muse / parser / pilot | 2.387 | 5.256 | 1 | 1 |
| Muse / parser / repeat | 2.515 | 8.303 | 1 | 1 |
| Muse / cache / repeat | 5.319 | 6.335 | 1 | 1 |

All 20 implementation arms passed their assertions. Aggregate implementation time:
Qwen 105.606 vs 135.229 s (~22% less with KEEP), MiMo 10.275 vs 19.886 s,
Muse 10.221 vs 19.894 s. Qwen used 4 vs 6 model calls. Cache-repeat pairs run
DROP first; parser pairs run KEEP first. Pilot cache also ran KEEP first.
Order is partially balanced, not randomized. There are only two fixtures, model
generation varies, and provider load/cache are uncontrolled. Do not generalize
these ratios to real projects or billing. Local cache telemetry did not establish
a cache-hit speedup.

Exact models: qwen3.8-27b-uncensored via LM Studio,
mimo-v2.6-flash-free and muse-spark-1.3-contributor-free via OpenCode Zen.
deepseek-v4-flash-free returned HTTP 400, model unavailable, on its first request.
It was not retried or included as a successful quality measurement.

## Integrated implementation verification

The A/B harness replaces only history in the real transport, so separate checks
exercise the actual updated Loop, portable SQLite persistence and reconstruction:

- Muse: 2.711 s first turn, 3.431 s continuation; native request counts [0,1],
  saved native counts [1,2]. Generated parser passes 9/9 assertions.
- Qwen: the first integrated attempt had a 1024-output-token/60-second limit
  and failed to produce visible replies. It is not counted as successful.
  The diagnostic rerun used the A/B budget of 3072 and a 120-second limit:
  67.006 s first turn, 29.892 s continuation; native request counts [0,1],
  saved native counts [1,2]; parser passes 9/9 assertions.
  First-turn reported usage: 523 input, 1887 output, 1829 reasoning.
  Second-turn usage: 2569 input, 611 output, 469 reasoning.
  The small output allowance in the initial smoke was insufficient for that
  generated plan; the rerun also raised timeout, so this is not a single-variable
  latency comparison. Production timeouts/output budgets were not changed.

Unit/integration regressions cover native chat fields; encrypted Responses items;
GUI database close/reopen; TUI-style resume; display preservation; scope isolation;
token accounting; and native reasoning not counting as a visible final answer.

Anthropic has protocol tests, not a live Claude quality comparison. Preserve the
whole native assistant block order, including text and tool input between thinking
blocks. A local hash binds replay to the original serialized system/tools/history.
When that prefix changes, omit signed thinking from the request while preserving
visible text/tool protocol and the archive. This is deliberately conservative:
providers/models that tolerate edits may lose reusable thinking too. Dynamic
reminders, mode changes, tool discovery or client compaction can cause that fallback.
This prevents sending known-invalid signed state; it does not claim universal
append-only Claude prompt construction or live validation on every Claude model.

Existing archives containing only displayed thinking cannot recover an encrypted
payload that was never saved. The legacy text-tail opt-in remains separate.
OpenRouter-style reasoning_details arrays are not newly implemented by this change.

## Lightweight startup without a conversation dead end

Keep a small initial context for obvious casual conversation. The former prompt
told users to repeat project requests or edit navigator configuration. Its shorter
replacement permits discovery. After successful tool_search, the same turn restores
project instructions and the normal tool set, including discovered tools. No
classifier inference or repeated user message is required. Direct recall/web lookup
and failed discovery do not expand the route.

The tool_search description now says to use available tools directly and discover
missing ones; it no longer forbids searching for file tools that are absent from a
light request. Tests cover chat/advisor discovery → execution → reply, deferred
repository context, failed discovery, and GUI restart.

## Inspiration and references

Pi's openai-completions.ts, openai-responses-shared.ts and transform-messages.ts
preserve native reasoning fields/items instead of turning them into prompt prose.
No Pi source code was copied.

- [Qwen3.8 preserved thinking](https://huggingface.co/Qwen/Qwen3.8-27B#disable-preserved-thinking)
- [Anthropic preserved thinking and prefix binding](https://platform.claude.com/docs/en/build-with-claude/preserved-thinking)
- [DeepSeek thinking protocol](https://api-docs.deepseek.com/guides/thinking_mode/)

Reproduction sources are in scripts/reasoning-history. Local raw fixture-only
requests, SSE and results remain in .tmp/history-eval/{pilot,pilot-cache,pilot-cloud,
repeat-local,repeat-cloud}. Integrated runs are in the same parent folder.
Transport selection, OpenCode public-client headers and special Zen request
preparation are unchanged.

Scope filtering also runs before context estimation and history projection. A
regression fixture with 50000 tokens of incompatible old-model reasoning verifies
that switching models does not schedule a handoff summary for data the transport
would discard anyway. The saved history is retained.

## Final validation and installation

All 64 tested packages passed (`go test ./...`); `go vet ./...`,
`git diff --check`, and Node syntax checking passed. Both GUI/TUI builds and
`--help` smoke checks succeeded. Installed EXEs match the staged binaries byte
for byte. Previous binaries: `.tmp/previous-binaries-Iw8i09`. Hashes and exact
installation details: `.tmp/history-eval/install.json`. Dedicated Zen transport
files and opencode.go have no diff. Restart a running application to load the
new binaries.
