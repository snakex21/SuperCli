# Compaction uses projected visible history — 2026-09-27

## Confirmed failure

After the hidden-history fix, compaction still chose its turn boundary and priced
its replacement using native reasoning retained in canonical messages. Ordinary
requests already remove completed reasoning under the user's discard preference,
and filter incompatible state after changing model/endpoint.

Six regression cases failed before this change:

- Manual compaction accepted a replacement based on 100k estimated tokens of
  reasoning that the outgoing request did not contain. In both discard-completed
  and different-provider fixtures, the actual request estimator increased from
  305 to 864 tokens after the purported reduction.
- Large unsent reasoning in the recent tail, or a large hidden recent answer,
  forced the full-history branch. The last correction and current tool exchange
  were then summarized despite their small visible size.
- A hidden obsolete user message counted as the previous user turn, displacing
  the last real visible correction from the protected tail.

Evidence: .tmp/compaction-projected-history-2026-09-27/red.json.

## Implementation

A compaction-only view filters hidden messages, applies the existing completed
reasoning preference to the full visible conversation, and applies the existing
provider/model/scope filter. It records original indices when needed, allowing the
selected visible boundary to replace the correct canonical prefix.

Manual, automatic and model-switch compaction all use this view for turn selection
and the reduction check. No change is made to the provider serializers, Zen
dialect, ordinary request path or saved archive. Tool evidence is intentionally
retained for the summarizer.

Two short visible turns are protected instead of falling through to summarize
the previous turn after hidden messages disappear. Useful explicit compaction of
two long turns remains available: the fallback applies when visible history
exceeds half the window or the fixed estimated size of the largest standard
summary (existing text/facts caps plus wrapper). Truly oversized current turns
retain the emergency full-history path.

This work addresses hidden/native-state overcounting. It does not claim that
every possible difference between summarizer evidence and outgoing tool-history
projection has been eliminated.

## Verification

- The six failing cases now pass. Ineffective replacements are rejected without
  changing history; protected recent messages and active call/result pairs remain
  byte-for-byte intact.
- A real OpenAI provider object's matching scope retains required tool reasoning
  and active reasoning; the completed-reply keep/discard preference remains
  reversible, and projection does not mutate native archive data.
- Existing manual/automatic/model-switch, hidden-history, persisted resume,
  TUI slash, GUI endpoint and protocol tests pass.
- Tests distinguish two short turns, a large previous turn, a large current turn,
  and one genuinely oversized turn.
- The GUI endpoint regression initially exposed an over-conservative fallback
  that prevented useful manual compaction of two long turns. Production logic
  was corrected; that GUI fixture was kept unchanged.
- The prior agent insufficient-reduction fixture now includes an older turn
  outside the two protected recent turns so it still exercises the reduction
  rejection rather than returning before calling the summarizer.
- Full go test -timeout=90s ./..., go vet ./..., CLI build and GUI build pass.

## Cost and limits

No additional model calls, persistent instructions, source reads or per-request
work were added. The extra view is constructed only when compaction is requested
or its existing trigger fires. Estimator numbers above are deterministic
regression evidence, not provider-billed token measurements or a latency benchmark.
