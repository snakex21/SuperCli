# Context handoff and search neighborhoods — 2026-09-23

## What changed

The shared agent loop recognizes a change of the model or configured provider that
actually consumed the coordinator context. GUI loops restored for every turn and
the persistent TUI loop use the same implementation. The selected picker value is
not treated as proof that a model ran.

For a changed model, a replay above approximately 32,000 estimated tokens triggers
one summary of older completed turns. The model window and an existing learned
prefill budget can lower this trigger. This is not a new context-window limit:
the two latest user turns, including current tool calls, results and attachments,
remain intact even when they exceed the trigger. Small conversations and ordinary
continuation with the same model do not gain a summary call. Light chat routes do
not incorrectly mark the full coordinator history as consumed.

Existing sessions without the new model identity are checked on first reuse.
The identity is stored in the portable sessions database after a successful
coordinator call; subsequent calls with that identity need no extra write.
The durable model projection retains the summary across reopening and model
changes. The complete archived transcript remains available to UI/export/history
search. No model call runs just because the picker or a session is opened.

A failed/empty/non-reducing handoff summary keeps history unchanged. An attempt
is not repeated at each step of that handoff. Existing hard-window overflow
recovery remains separate. Older TUI /resume no longer uses its own eager
summarization and blind truncation fallback: the shared loop prepares context
after the next user instruction arrives.

Switching models also resets the previous model's token-count calibration and
retained reasoning. Saving a context projection now copies image-bearing message
references before marking their stored images dormant, preserving current images
for the actual model request.

Compaction transcript rendering caps large tool arguments as well as results at
700 bytes each, retaining the beginning and end with an explicit omission marker.
This avoids replaying full patch bodies into the summary call and preserves
terminal error/status text at the end. Original messages are not changed.

The common summarization prompt was not lengthened. Provider transport code and
special OpenCode Zen request/header handling were not changed.

## Cost and limitations

Creating a summary is still an inference call and can take time. This change does
not transfer GPU KV cache between models, make the first large switch instantaneous,
or guarantee that a model's lossy summary retains every historical detail. There
is no live-model speed or quality claim from the regression fixtures.

The 32k threshold is a conservative common replay trigger, not a measured optimal
value for every backend. Current work is protected rather than forcibly truncated
to that number. A large latest turn can therefore still have slow prefill.
Very long user/assistant text also remains in the summary input; tool-body bounds
do not promise an arbitrary history will fit a smaller summarizer window.

## Search change completed in the same turn

search_code accepts optional context=0..20. Default 0 preserves location-only
results. A nonzero radius adds numbered neighborhoods, merges overlapping ranges,
and marks matches with >. The match limit still counts matches, not context lines.
The result is bounded at 500 neighborhood lines, 2048 bytes per line; clipping
and unavailable context are explicit. Both ripgrep and the Go fallback use this
renderer. Searches are unchanged unless the caller requests neighborhoods.

Read-only review of one saved session segment found 22 searches, 13 followed at
the next assistant tool-call message by read_lines/read_many. This is evidence of
an opportunity, not proof that all those calls or model turns were avoidable.

On a 40-file fixture, median local search execution was 0.313 ms with context=0
and 0.502 ms with context=8 (three samples each). The approximately 0.19 ms extra
local cost can avoid a separate read round when the neighborhood answers the
question. It is not a measurement of model latency or provider billing.
The serialized compact tool definition shrank from 699 to 676 bytes including
the new argument. Byte count is not an exact tokenizer-based cost measurement.

## Verification

- Full go test ./...: all 64 tested packages passed.
- go vet ./... and git diff --check passed.
- Regression coverage: local/cloud scope parity, one handoff vs warm continuation,
  small-context no-op, preserved latest instructions and unresolved tool failures,
  failed summary retaining history, durable projection and model identity, metadata
  reopening/truncation/deletion, hidden history exclusion, active image preservation,
  deferred TUI resume, and UTF-8-safe bounded summary inputs.
- Search coverage: unchanged default output, merged neighborhoods, result limits,
  cancellation/missing files, ripgrep/fallback parity, fallback after ripgrep failure,
  and Windows/colon path parsing.
- The synthetic handoff fixture uses a deterministic summary stub. Its replay
  estimate changes from 61,971 to 279 tokens; this checks mechanics and boundaries,
  not summary quality or real-world achievable reduction.

Local evidence: .tmp/context-handoff-checks.json,
.tmp/handoff-focused-tests.json and .tmp/search-context-benchmark.json.

## Installed artifacts

Both CLI and GUI binaries were rebuilt, passed --help smoke checks and installed
with byte-for-byte verification against the builds. Previous binaries are in
.tmp/previous-binaries-Gop4lk. Install evidence: .tmp/context-handoff-install.json.
Restart an existing application process to load the new executable.
