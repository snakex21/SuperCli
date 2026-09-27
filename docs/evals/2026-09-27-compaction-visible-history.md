# Compaction respects hidden history — 2026-09-27

## Evidence

The prior turn fixed deterministic file facts. Inspection of compaction's input
then found a separate visibility bug:

- Automatic and manual Loop compaction passed AllMessages into the summarizer,
  including messages already excluded from model context by HideRange/clear.
- Model-switch compaction already filtered its input, but the shared replacement
  reset every hidden flag. Hidden messages in the retained tail became visible
  again, and that revived projection was persisted.
- TUI /compact bypassed Loop.CompactNow altogether: it directly summarized every
  message and replaced the full conversation, ignoring the configured summarizer,
  recent-turn protection and the reduction guard.
- The reduction check counted leading system instructions as if removed, although
  they are retained. A longer replacement could therefore be accepted as a saving.

The initial suspicion of replayed inline thinking was not supported by the fixed
recent-session sample: 0 of 455 assistant messages in the 1815-message sample had
inline think/thinking blocks. Existing cleanup already handles that case; it was
not changed.

## Before/after reproduction

A deterministic fixture has a large hidden older turn, visible old findings,
a hidden recent answer, and a retained current tool pair.

| Path | Before: bytes sent for summary | After |
| --- | ---: | ---: |
| Manual Loop compaction | 225095 | 25056 |
| Automatic compaction | 225095 | 25056 |
| Model switch | 25056 | 25056 |

All three paths previously revived the hidden recent answer. All now preserve
its visibility flag while keeping the actual tail messages and tool pair intact.
The session-store test verifies both the saved model projection and a resumed
loop; the full UI archive still contains the original messages.

When the replaceable prefix is entirely hidden, the old manual/automatic paths
made a summary call. Both now make zero calls. These are deterministic fixture
results, not live-provider latency measurements or an assertion that all sessions
get the same reduction.

The actual TUI slash handler was tested before changing it: the configured
summarizer was called zero times, and both success and failure fixtures instead
went through the old direct path. After the fix the configured summarizer is used
once; recent instructions survive and a summary failure leaves context unchanged.

Evidence is under portable .tmp/compaction-visible-history-2026-09-27:
red.json, slash-red.json, focused.json, parity.json, suite-final.json and
vet-final.json.

## Implementation

The existing hidden-prefix filter is now shared by all compaction entry points.
Compacting a prefix remaps hidden flags for the retained leading messages and
tail, drops flags for replaced entries, and persists the resulting projection.
An empty replacement does nothing.

The reduction guard counts only the non-system prefix that is actually replaced.
The TUI command calls CompactNow, sharing the configured side-provider/fallback
policy, facts, boundaries and error handling with GUI. The now-unused app-level
wrappers were removed; memory autosave retains its transcript helper.

No prompt instruction, provider dialect, model call or regular-turn scan was
added. The visibility work runs only when compaction is already being considered.
Original messages remain available in the session archive.

## Validation

- Red/green tests cover manual, automatic and model-switch paths, a wholly hidden
  prefix, retained system cost, persisted/resumed visibility and real slash wiring.
- Existing recent-turn, unresolved tool pair, image, failure, context-window and
  model-handoff tests pass.
- Full go test -timeout=90s ./... and go vet ./... pass.
- CLI and GUI builds pass.

The first full suite exposed a GUI fixture whose Echo response was not actually
smaller than its replaced history. Previously the retained system prompt masked
that. The fixture now supplies enough old history for real reduction, preserving
its original assertion that configured web compaction uses a summary and retains
the current request. No production guard was relaxed to satisfy the test.
