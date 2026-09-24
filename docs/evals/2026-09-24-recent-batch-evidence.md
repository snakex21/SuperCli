# Retain small evidence from mixed tool batches

Date: 2026-09-24.

## Problem and implementation

Completed tool history already has a 4096-byte recent-evidence allowance. The
previous projection retained a whole assistant tool-call batch only when all
its arguments and results fit. A large read therefore removed useful small
results issued alongside it, even when the small exchange would fit.

A size-only audit of the latest 200 stored assistant messages with tool-call
JSON found 69 mixed batches over 4096 bytes with at least one potentially
fitting exchange. For example, session 97f11945104d6cb4 seq 500 combined an
approximately 1963-byte search exchange and 4639-byte read_many exchange.
This is historical evidence of the shape, not a count of affected current
follow-ups: it does not apply the latest-turn, remaining-budget, multimodal
or native-state eligibility checks, and transcripts predate earlier fixes.

The projection now retains complete small call/result pairs from an otherwise
oversized complete batch. Smaller exchanges get retention priority; original
call order and result chronology remain unchanged. Assistant text and arguments
count toward the same 4096-byte limit. Selection allocates a separate tool-call
slice and never mutates the canonical or persisted conversation.

Unchanged guards:

- Only the latest completed user turn contributes bounded recent evidence.
- In-progress tool chains and native reasoning continuation state stay intact.
- An unavailable transcript, missing writer/history tool, or persistence outage
  disables this projection as before.
- Malformed/incomplete batches cannot enter partial retention.
- Images and reasoning parts are not reattached through this path.
- Older/large results remain available through search_history.
- No new instruction, tool schema, extra inference, cache, provider branch or
  settings. OpenCode Zen's transport and special handling are untouched.

## Controlled model comparison

scripts/recent-batch-evidence constructs the same mixed read_lines batch from
two real synthetic files: a build log and src/retry.go. It persists the initial
conversation, then initializes the real agent loop for the follow-up. The
previous final answer contains file names only. After that read, the fixture
changes RetryDelay/MaxAttempts from 235/7 to 9001/99.

Two follow-ups distinguish historical recall from current-state verification.
Only read tools and the synthetic session store are exposed. Baseline binaries
use a Go source overlay of the original projection. Qwen ran baseline first;
Muse ran candidate first. There is one before/after pair per model/scenario.

| Model | Follow-up | Calls before -> after | Tool attempts before -> after | Input tokens before -> after | Seconds before -> after | Correct |
|---|---|---:|---:|---:|---:|---|
| Qwen | historical values | 3 -> 1 | 3 -> 0 | 10597 -> 1956 | 34.155 -> 11.299 | both |
| Muse | historical values | 2 -> 1 | 3 -> 0 | 7988 -> 2233 | 19.703 -> 4.448 | both |
| Qwen | current values | 2 -> 2 | 1 -> 1 | 5540 -> 5662 | 15.949 -> 10.802 | both |
| Muse | current values | 2 -> 2 | 1 -> 1 | 6024 -> 6071 | 8.945 -> 43.257 | both |

For historical recall, both candidates answered with the original 235/7
without retrieval. Baselines searched history and also reread current files.
For current state, both versions/models executed read_lines and answered
9001/99. Retaining evidence did not substitute an old snapshot for that read
in this experiment; no universal model behavior is guaranteed.

The first request now contains one preserved tool result instead of none:
serialized messages plus tool definitions grew from 6621 to 7044 bytes for
recall, and 6549 to 6972 for current-state questions (+423 bytes). This is a
bounded context tradeoff, not zero input cost. The current-state controls show
slightly more input without a saved call.

These are controlled follow-up scenarios, not end-to-end real project sessions
or a statistical latency estimate. Native reasoning was not seeded into the
fixture; compatible native chains already bypass this projection. Wall times
include variable backend latency and output length; the Muse current-state
result explicitly prevents a claim of universal acceleration. Raw input totals
include cached input and are not monetary estimates.

## Validation

- Regression failed before the change: the small 235/7 result vanished.
- Forty deterministic mixed-batch layouts verify the 4 KiB total allowance,
  argument/text costs, valid pairs, stable repeated projection and no mutation.
- Both native tool and sentinel agent routes receive the preserved small result.
- Coverage includes large arguments/assistant text, missing or duplicate
  results/IDs, image results, native reasoning and the active tail.
- Full go test -timeout=90s ./... and go vet ./... passed.
- CLI/GUI builds, startup checks, diff validation, installation hashes and backup
  locations are recorded with the experiment.

Local evidence is in .tmp/recent-batch-evidence/: baseline source overlay,
size-only log audit, regression output, protocol/full tests, raw model answers,
tool attempts, request sizes, comparison.json and installation record.
