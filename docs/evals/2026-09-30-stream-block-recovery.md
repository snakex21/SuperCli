# Closed malformed tool blocks no longer hide later calls

Date: 2026-09-30

## Problem

The shared agent stream scanner disabled XML or sentinel parsing for the rest of a response after a complete block parsed to zero tool calls. A valid block of the same protocol later in that response was surfaced as text instead of being executed. This was reproduced from the parser code with a deterministic provider fixture; its frequency in real sessions was not measured.

## Change

Emit the closed malformed block as ordinary text, advance to the unconsumed suffix, and continue inspecting complete blocks. Keep partial blocks buffered until completion or the end of the stream. Parsed calls still use the existing registry, argument validation and execution path.

The shared loop is used by GUI and TUI. There are no extra prompt instructions, model requests or provider-specific transport changes in this fix.

## Replay evidence

| Fixture | Before | After |
| --- | --- | --- |
| Malformed XML followed by valid XML read | Read not executed | Read executes once |
| Empty sentinel followed by valid sentinel read | Read not executed | Read executes once |
| Normal or thin tool mode | One response ends without the requested read | First response supplies the read; the second returns the final answer |

The two requests after recovery are the ordinary tool-call/final-answer sequence. This does not demonstrate a lower wall time than the prematurely finished baseline. It avoids discarding an already generated valid call and needing a separate corrective attempt. No live provider latency or token savings were measured.

## Validation

- Regression tests cover all byte split positions for both protocols, mixed protocols, repeated malformed blocks, exact streamed text and usage preservation.
- A loop-level replay verifies one execution and one tool result in the next request in both normal and thin mode.
- Byte-at-a-time malformed and incomplete output stays literal text and executes no tools.
- Full project tests passed: `go test ./... -timeout=90s`.
- Static analysis passed: `go vet ./internal/agent`.
- Focused stream and read-mirror tests passed with `go test -race`.

All replay tests use deterministic in-process providers; no paid or local inference calls are required.
