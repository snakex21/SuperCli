# GunMayhem session audit — 2026-09-25

## Scope and evidence

Read-only inspection of the recorded 2026-09-24 Cloudflare multiplayer task in GunMayhem Go (session de105c910d16e47e, through message 546). Source: portable sessions.db, session_turns, session_usage, stored task results and their retained tool observations. No replay of project-changing commands and no provider calls. Later conversation/active work was excluded.

The first recorded turn lasted 8,388.8 seconds (2 h 19 min 49 s). It contains 107 coordinator steps, 382 model calls including delegates, 439 coordinator tool calls and 23 coordinator tool failures. There were two failed model calls and one canceled call. The final recorded assistant message before the user's follow-up is another search call, not a final report. The coordinator reported completion only after the user asked for status.

## Largest cost

- A simultaneous review + plan delegation blocked the coordinator from 19:48:38 to 21:09:59 UTC: about 81 min 20 s, or 58% of the recorded turn. Both prompts covered the same Cloudflare, netplay, UI, tests and packaging files, with substantially overlapping deliverables.
- The review worker reported 212 steps and 417,178 output tokens. Its evidence attachment retained 16 observations and explicitly omitted 785 older observations: at least 801 tool observations, excluding some discovery/empty results.
- The plan worker reported 21 steps and 34,720 output tokens. Its attachment contained 57 retained-plus-omitted observations.
- A later review failed with a provider stream error after 42 steps. Its attachment still held recent observations; it returned no final report.
- The tool-name summary in a task result currently counts only the first 32 tool-call events. It must not be interpreted as the total work performed.

Worker observations are bounded to 64 KiB/32 items. The complete worker conversations and earlier observations are not persisted, so the evidence cannot establish which of all 801 observations were redundant. The retained end shows further searches and reads across menu, discovery/DHT, P2P and tunnel code. A long review alone is not proof that every read was unnecessary.

## Model work versus harness overhead

The completed-turn usage ledger records about 74.09 million cumulative input tokens, 70.31 million cached input tokens (~94.9%), and 738,244 output tokens, including 607,939 reported reasoning tokens (~82.4%). These are cumulative billed/reported call totals, not one context window. Reasoning is already included in output and must not be added again. This does not establish the quality or necessity of individual reasoning steps.

Coordinator phase counters: context preparation 0.077 s, next-turn preparation 0.278 s, request encoding 1.694 s, persistence 0.737 s, backend wait 561.897 s, streaming 2,150.491 s, tool execution 5,673.579 s. Individual parallel tool durations overlap and must not be summed as wall time. These counters point to model/worker work, not millisecond-scale startup overhead, as the main opportunity in this case.

## Reproduced and fixed defect

Three ctx_execute calls (messages 367, 368, 374) were rejected because max_stdout_kb exceeded 64. The model retried an oversized preview request (including 3000 KB). The command runner already clamps both stdout and stderr preview limits to 64 KB, but JSON-schema validation rejected the request before the runner could do that.

Removed the schema's rejecting upper bounds for these two preview parameters and described their existing clamp. Positive-integer validation, execution safeguards, timeouts, retained-output limits and the runner's hard 64 KB preview ceiling remain in force. Command arguments are not rewritten or retried automatically. This shared tool serves GUI/TUI and local/cloud providers. No provider transport or system prompt changes.

A subprocess regression test goes through Registry.Execute and produces more than 64 KB on both streams. Before the fix, oversized requests fail schema validation. After the fix, they execute successfully, preserve tail evidence and truncation flags, and both streams remain bounded. Smaller explicitly requested caps still apply independently.

Validation: go test ./internal/tools/workflow ./internal/tools/ctxexec ./internal/tools/core -timeout 90s passed. CLI and GUI builds were also verified.

## Next optimization target and limits

The largest target is useful delegation: distinct bounded deliverables, returning findings once enough evidence exists, and continuing an existing worker when its context is relevant instead of starting another overlapping review. This case does not justify an arbitrary step cutoff, silently lowering reasoning effort, or truncating unseen evidence. The preview fix removes a proven error/retry cause, but no end-to-end speedup for the two-hour task has been measured and it does not solve the 212-step review by itself.
