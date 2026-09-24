# Stable conversation prefix with refreshed memory — 2026-09-23

## Reproduced problem

GUI reconstructs the agent loop for each user turn. Its memory briefing and
folder-index snapshot were appended to the leading system prompt. An updated
remembered preference therefore changed bytes before the entire conversation.
The regression test failed on the original code with:
"changing remembered preference rewrote the prefix before the conversation".

## Change

LoopConfig.LiveContext carries current snapshots at the existing request tail,
beside the freshness stamp. GUI supplies memory and folder-index information
there instead of inserting them into the stable system prefix.

This common agent mechanism applies to coordinator, chat, advisor and clarify
routes, using both native and thin tool profiles. Current information appears
once, does not accumulate in the saved transcript, and is included in context
budget estimates. GUI light routes now also receive the memory snapshot; they
previously replaced the system prompt without receiving its memory briefing.

The TUI's existing session-start memory snapshot remains unchanged. Its stable
prefix does not need to be rewritten. No local/cloud provider branch, new model
instruction, tool schema, helper inference or transport change was added.
The special OpenCode Zen request dialect and headers are untouched.

This specifically fixes prefix changes caused by memory/folder refreshes. Changes
to core instructions, tools, active goals, model settings or the conversation
projection can still change the prefix. Cache eviction and provider caching
policies also remain outside this change.

## Live local measurement

An explicit opt-in test used the already loaded qwen3.8-27b-uncensored in LM Studio
on the user's machine. It sent four tool-free requests containing only a synthetic
fixture and a CURRENT_TAG value. Reasoning was disabled for this test process;
no application settings, model-server settings or repository files were changed
by the model. Each request allowed at most 24 output tokens.

The fixture contained approximately 9,600 input tokens. Both layouts sent exactly
37,735 text bytes and reported the same input/output token counts for matching
tags. The second request within each pair changed only ALPHA to BRAVO.

| Placement | Request | First text | Total | Answer |
| --- | --- | ---: | ---: | --- |
| Memory in leading prefix | ALPHA | 12,340 ms | 12,427 ms | ALPHA |
| Memory in leading prefix | BRAVO | 10,488 ms | 10,562 ms | BRAVO |
| Memory in request tail | ALPHA | 10,459 ms | 10,526 ms | ALPHA |
| Memory in request tail | BRAVO | 381 ms | 446 ms | BRAVO |

All four answers used the correct current value. In this one controlled run,
time to first text after the update fell from 10.488 s to 0.381 s.
The initial request with the new layout still needed 10.459 s.

This is one four-request synthetic test, not a distribution or a whole-task
speed benchmark. The usage frames did not expose positive cached-token counters;
the report makes no claim about an exact number of reused KV tokens or cloud
billing savings. It measures response latency and equal prompt size. Reuse of a
stable prefix still depends on the server retaining a usable cache.

Raw evidence: .tmp/context-prefix-live.json and
.tmp/context-prefix-live-run.json.

To rerun deliberately, set SUPERCLI_PREFIX_EVAL_URL to a local OpenAI-compatible
endpoint, SUPERCLI_PREFIX_EVAL_MODEL to the loaded model ID, and
SUPERCLI_PREFIX_EVAL_REPORT to an app-local report path, then run:
go test ./internal/agent -run '^TestContextPrefixLMStudioLive$' -count=1 -v

Ordinary test runs skip live inference.

## Verification

- A GUI integration regression captures the provider-bound messages before and
  after updating a real memory-store entry. The tool definitions and conversation
  prefix remain identical, the latest fact appears exactly once, and each turn
  makes exactly one provider call.
- Shared-loop tests cover all four routes, native/thin profiles, unchanged prior
  messages, no stale snapshot, no accumulation in canonical history, and correct
  accounting for the snapshot's token cost.
- Full go test ./...: all 64 tested packages passed.
- go vet ./... and git diff --check passed.
- Local records: .tmp/context-prefix-before.json,
  .tmp/context-prefix-after.json and .tmp/context-prefix-checks.json.

Both executables were rebuilt, passed --help smoke checks, and installed with
byte-for-byte verification. Previous binaries: C:\Users\ASRock\Desktop\SuperCli\SuperCli\.tmp\previous-binaries-1bUfq0.
Install record: .tmp/context-prefix-install.json. Restart the application to
load the new executable.
