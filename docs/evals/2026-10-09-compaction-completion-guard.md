# Compaction completion guard — 2026-10-09

The summarizer previously accepted any nonempty text, including a response explicitly stopped by the output-token limit. The manual compaction regression reproduced replacement of two older messages with an incomplete Goal/Done fragment. This is a demonstrated correctness gap; it is not evidence that a particular historical task repeated work because of it.

## Final change

- Reject non-text tool calls and explicit non-success finish reasons, including length, content_filter, tool_calls and pause_turn. Valid stop, same-delta text/stop, and the existing clean legacy EOF convention remain supported.
- Remember a rejected result and drain the remaining stream. Trailing usage reaches Metered before compaction returns. Further unusable text is not accumulated. Caller cancellation remains immediate; a transport error cancels the helper request.
- A configured side summarizer may fall back once to the active model on an invalid result. A healthy result still uses one call. Caller cancellation does not start a fallback call.
- ClampSummary keeps UTF-8 rune boundaries at the existing 4,000-byte cap. The newline preference, ASCII behavior and truncation marker remain unchanged.

The shared summarizer is used by GUI, TUI and batch owners. No normal-turn prompt, tool schema, provider route, additional model call or persistence worker was introduced. The OpenCode Zen special route was not changed.

Manual compaction leaves both stored and visible history unchanged on a rejected summary. Emergency automatic compaction retains its existing fallback policy, including protection of the two recent visible user turns; this change does not make that fallback preserve all older history.

## Discriminating verification

The new permanent tests are in internal/agent/context_summarize_completion_test.go. They cover the actual OpenAI SSE parser, normal/truncated finishes, tool fragments, late usage through Metered, cancellation with an open or closed provider channel, configured fallback call counts, unchanged manual history, and two/three/four-byte characters crossing the byte cap.

Before the production patch, the isolated regressions failed: partial text was accepted, manual history was replaced, caller cancellation could wait on a still-open channel, canceled work could call a fallback, and clamping split a UTF-8 rune. Positive completed-result cases passed.

An independent read-only review caught an accounting regression in the provisional early-return implementation. A real SSE fixture flushing length before a usage frame delayed by 20 ms reproduced TokensIn=0, TokensOut=0 and Canceled=true. The final implementation drains accounting; length and stop both preserve input 123, output 45, cached 23, reasoning 10 and purpose compact. This provisional version was not installed.

Final focused regressions passed three times. Fresh complete tests and go vet passed for internal/agent, internal/llm, internal/llm/factory, internal/app, internal/webgui and internal/ui/tui. The earlier unrelated suite is preserved by an exact source proof; it was not rerun and is not reported as a fresh full-suite run here.

Portable evidence: .tmp/optimization-oct8-2026/compaction-completion-audit; go-compact-completion-before-oct9-0.log; go-compact-accounting-before-oct9-0.log; go-compact-final-oct9-j-{0,1,2,3}.log. Read-only final review: compaction-completion-audit/review-final.json.

## Local overhead measurement

A matched synthetic completed-summary microbenchmark uses the same provider channel, transcript and completed text; no network or inference is involved. Three runs, 200 ms each, Windows AMD64, GOMAXPROCS=2:

| Version | Median ns/op | B/op | Allocations/op |
| --- | ---: | ---: | ---: |
| Original | 871 | 2,832 | 9 |
| Final completion/cancellation guard | 1,241 | 3,040 | 12 |

The additional local cost is about 0.37 microseconds and 208 allocated bytes per compaction helper call. It is not a speedup of inference or evidence of improved tokens/second, RSS, or fewer turns on a real task. There was no live Qwen, Zen or Kilo inference in this batch.

## Source and binaries

Current covered-source SHA-256: b13a77390fb0179ff98c6247e843ec34b260389984e236e3dc1559c56fc76df3.

Replacing the two changed prior files with their saved originals and excluding the new regression file reproduces the fully validated previous snapshot 80f7e3a68f5659a387509bc5e383e2c16de265850abbec764aefe5862d5a277e. The other prior file is the transcript performance verifier, a test-only correction documented separately. The only newly changed production source is context_summarize.go.

GUI and TUI cross-builds succeeded for Windows AMD64, Linux AMD64/ARM64, and macOS AMD64/ARM64. This is compilation coverage, not Linux/macOS runtime validation. Both Windows EXEs were installed with verified hashes and recoverable backups inside the workspace. Both --help smoke checks exited successfully. No user application was killed or restarted.

- supercli.exe: 26,915,840 bytes; ce2286b0f7e33f3e26c5a8f03972e3b16e3ebec58b328a7a358fe96bd5f7e83d.
- supercli-web.exe: 23,837,696 bytes; fac83aee7f81c35cb0973a3c726755c945f585222c6812a97d555dc74f69dfbb.

All measurement output, build caches and backups remain under the application/workspace directory. No commit, push or release was made in this batch.

## Remaining candidate

The completed checkpoint preserves late worker changes, but failure of the SQL telemetry update has no durable correlation/retry mechanism after restart. A source audit is in deferred-telemetry-audit/audit.json. Matching an old assistant summary by reused sequence, Latest or timestamps is unsafe. Exact on-demand display can be added using the immutable original user receipt; exact summary repair would require a durable correlation key in both existing writes. This remains a separate scoped candidate, not an implemented optimization or a reason to add a background polling queue.
