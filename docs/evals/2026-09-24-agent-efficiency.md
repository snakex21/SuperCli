# Fewer avoidable steps: lookup, delegation and recent evidence

Date: 2026-09-24. Scope: the shared agent/tool implementation used by CLI/TUI
and GUI. OpenCode Zen transport, headers, gate tools and dialect are unchanged.

## Changes

- Hard orchestrator mode can directly use search_code and read_many; both are
  schema-core. search_history is available for discovery. It still delegates
  file mutations and command execution. Its tool_search now searches/activates
  the restricted registry instead of advertising tools owned only by workers.
- Existing coordinator/task instructions were rewritten around direct targeted
  lookups, independent work that can overlap, substantial isolated exploration,
  and continuing an existing worker. This is guidance, not a prediction that
  every delegation will save time. No new classifier model or forced worker
  quota was added. Coordinator prompt bytes: 963 -> 949; orchestrator: 858 -> 679.
- A content search without explicit context now includes four neighboring lines
  for at most three hits, provided the search was not capped and the expanded
  result fits 2048 bytes. Broad/capped/large results stay location-only;
  context=0 explicitly requests the previous format. Filename discovery is
  unchanged. Sparse searches pay for bounded neighborhood reads; broad ones do
  not. Explicit context retains its existing controls.
- When completed tool history is eligible for omission, small whole call/result
  batches from the latest completed user turn survive in their original order.
  The shared budget is 4096 bytes of content, arguments and envelope allowances,
  not an exact tokenizer/wire-byte limit. Older and oversized results remain in
  the transcript/search_history. A finished subsequent turn retires this window;
  it does not accumulate indefinitely. No summary inference or extra system
  instruction is required.
- Existing persistence-health and native-reasoning safeguards still apply:
  missing/unreliable transcript storage and native continuation chains are not
  newly stripped. Active tool exchanges are always retained. Multimodal content
  is not reattached by the recent-evidence path.
- Testing with actual ripgrep exposed a cancellation issue in its fallback:
  cancellation was converted into an untyped search error and attempted the
  fallback scanner. Cancelled searches now return the context error immediately.

The additional orchestrator schemas and bounded recent evidence have a prompt
cost. These changes trade a little reusable context for fewer rediscovery or
delegation round trips; they are not zero-token optimizations.

## Whole-turn comparison

Runner: scripts/agent-efficiency/main.go. Real agent loops and a separate SQLite
transcript per synthetic case. Models receive only lookup, history and delegation
tools; no command or file mutation tools. The harness alone changes fixture
values from 235/7 to 9001/99 to distinguish historical and current-state answers.

Models: LM Studio qwen3.8-27b-uncensored and Zen
muse-spark-1.3-contributor-free. Both use the same runner and application code.
Saved pre-change sources build the baseline with a Go overlay. All model calls,
including child calls, are counted. The fixture exposes full tool schemas;
separate unit tests verify the production thin-core partition.

The second paired run used the same explicit read-only wording in both versions:

| Model | Case | Model calls before -> after | Seconds before -> after | Correct |
|---|---|---:|---:|---|
| Muse | adaptive lookup | 2 -> 2 | 2.824 -> 6.543 | both |
| Muse | hard-orchestrator lookup | 4 -> 3 | 20.393 -> 14.078 | both |
| Muse | previous observed values | 3 -> 1 | 15.942 -> 10.637 | both |
| Muse | current values after change | 2 -> 2 | 4.431 -> 6.246 | both |
| Qwen | adaptive lookup | 2 -> 2 | 14.649 -> 33.330 | both |
| Qwen | hard-orchestrator lookup | 3 -> 2 | 23.940 -> 13.351 | both |
| Qwen | previous observed values | 2 -> 1 | 19.065 -> 11.638 | both |
| Qwen | current values after change | 2 -> 2 | 23.296 -> 25.976 | both |

On the historical-value case, both updated models used zero tools. Muse input
tokens fell 8573 -> 2746 (output 587 -> 745); Qwen input fell 5040 -> 2485
(output 302 -> 263). These cases have no child usage to exclude. Parent DoneEvent
usage must not be interpreted as total usage on delegated cases.

Current-state questions still caused a fresh file read and returned 9001/99.
The evidence policy does not silently cache filesystem results or forbid
legitimate rereads.

## Negative results and limits

The first pair is retained too. Muse hard-orchestrator lookup fell 6 -> 2 calls,
but its first updated historical-value attempt misunderstood the question as
restoring the file. It made six calls and attempted an unavailable command.
No fixture mutation was allowed. Although it reported the old values correctly,
this is a failed task-intent trial, not a success hidden from the results.
Both baseline and candidate were subsequently tested with the same explicit
"question about history; change nothing" wording.

The first updated Qwen historical answer included both old and current values,
which the initial simplistic checker incorrectly marked wrong merely because
9001 appeared. Manual review confirmed it labeled both states correctly, but
it still unnecessarily reread the file. The checker was corrected for the
second paired run; original output is retained.

Neither model reliably avoids every redundant check. Adaptive lookup already
used explicit search context in the baseline, so it did not lose a round trip.
Wall time is noisy and some unchanged-step cases were substantially slower.
These are small synthetic comparisons, not a general latency or quality
guarantee. Hardware load, backend caching and model output vary. Selection of
large tasks for profitable parallel delegation remains a model decision;
existing scheduling/worker-continuation behavior was not replaced.

## Validation and evidence

- Tests cover exact retained values, chronological position, bounded retention,
  older-turn retirement, whole tool batches, untouched canonical history,
  active results, native/persistence safeguards, image exclusion and restricted
  discovery ownership.
- Search tests cover automatic answer neighborhoods, explicit location mode,
  broad/capped/oversized results, cancellation, and ripgrep/fallback paths.
- Full go test -timeout=90s ./... and go vet ./... passed. The first all-in-one
  runner timed out before returning its results; a completed rerun is saved.
- CLI/GUI builds and installation are recorded separately in the evidence folder.

Local artifacts: .tmp/agent-efficiency/ contains baseline snapshots/overlay,
all live result JSON (including unsuccessful attempts), summary.json,
prompt-sizes.json and checks-retry.json. All fixture data remains beneath
the application directory.
