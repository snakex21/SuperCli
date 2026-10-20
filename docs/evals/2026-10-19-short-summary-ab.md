# Shorter compaction summaries: timing and retention experiment

## Decision

**Keep the existing default summary instruction.** A shorter task-state prompt
reduced visible output and sometimes reduced latency, but an additional live
smoke check failed to recover a readiness-history fact. The candidate therefore
did not satisfy the acceptance criterion and is kept in opt-in test code only.
No reasoning setting, model choice or API output-token budget was lowered.

The independent accuracy fix retained in production is a bounded historical
worker ID/status catalog, carried through subsequent compactions. It addresses
worker identities disappearing from both long and short model summaries.

## Setup

- Isolated copy of the existing 11,894-message session; source databases and
  portable OAuth login were not modified. Original conversation/memory files
  were opened read-only during fixture preparation.
- Live model/provider: Codex `gpt-6.1-sol`, session reasoning effort `xhigh` on
  every recorded wire request. The local LM Studio endpoint was unavailable;
  these results do not establish quality or latency on every other model.
- Shared production renderer, validators, exact instruction carry and host
  facts ran for each variant. A test decorator replaced only the helper's system
  instruction; transcript hashes matched in the paired timing tests.
- Three timing pairs in alternating AB/BA/AB order. Original and short prompts
  used independent cache scopes. Server load and generated reasoning length
  were uncontrolled; the sample is too small for a universal latency guarantee.
- A synthetic current task was appended to the real historical prefix. Its
  state changed across five compactions: corrected port/path, failed then
  recovered worker, launch versus verified/stopped process, resolved/new errors,
  cancelled documentation, implemented work and still-pending review.
- A model probe received only each resulting summary plus its exact host carry.
  Eleven independently specified fields were checked per round. Probes did not
  execute tools. Each round saved a SQLite projection, closed/reopened the
  store, and checked that restored context was identical and not duplicated.

## Experiments and final timings

The first candidate merely asked for 200 instead of 500 tokens, allowing a
larger response when necessary. It still generated about 500 visible tokens
and was slower (means: 22.93 versus 26.24 seconds). It was rejected.

The next candidate told the model that exact original user instructions,
legacy state and a file/tool catalog would be appended by the caller. It asked
the model to concentrate on continuation state, avoiding duplicate quotations
and catalogs. Its 200-token target was advisory; the existing byte/completion
guards remained and no truncating API budget was introduced.

An exploratory run showed a median reduction from 22.30 to 14.55 seconds, but
repeated compactions exposed omitted worker identities. Both prompt variants
could omit them. That prompted the deterministic worker-fact fix below. The
fixture was also made more faithful to actual generated task/process metadata.
The probe's ambiguous initial question about actual readiness was replaced by
one about verified readiness; unknown health is not asserted to be unhealthy.
Current completed worker sets replaced an unnecessary request for completion
ordering, which the final snapshot need not encode.

With the corrected fixture and worker facts, the last paired run was:

| Trial | Existing prompt, seconds | Short candidate, seconds |
| --- | ---: | ---: |
| 1 | 22.98 | 21.43 |
| 2 | 22.11 | 18.41 |
| 3 | 19.99 | 20.56 |
| Mean | 21.70 | 20.14 |

Mean visible output fell from **536.7 to 282.7 tokens** (about 47%). Mean total
time fell about **7%**, with one slower pair. Actual generated reasoning rose
from a mean of 336 to 516 tokens in this particular run despite the identical
`xhigh` setting, offsetting part of the visible-output saving.

For the four subsequent compactions in each retention chain, total helper time
was 89.72 seconds for the existing format and 65.93 for the candidate. Those
chains ran sequentially with their respective earlier summaries; these figures
are descriptive, not an independently randomized timing comparison.

## Retention outcome and rejected rollout

After the worker-fact fix, **both variants passed all five retention rounds**:
110 field checks in total, exact original instruction/legacy-state comparisons,
and five SQLite close/reopen checks per variant. No checked constraint, corrected
value, current error, cancellation or pending item was lost in those chains.

The candidate was then temporarily wired into the actual shared auto-summarizer
and an additional direct live smoke check verified its instruction hash and
unchanged effort. **That check failed on `readiness_ever_verified`.** The exact
original user carry remained intact. The failure proves that consistent recovery
of every tested state fact was not established; it does not identify whether the
failure arose in summarization or the probing model's interpretation. That run
recorded the failing field, not the full probe answer. No retry was used to turn
that result into a claimed success.

The tentative default change was removed. The standalone and auto-summarizer
retain their previous instruction and size/completion safeguards. The short
candidate remains available only in explicitly opted-in experiment tests.

## Accuracy change retained

`CompactFacts` now records IDs/statuses only from owned task/send-message
framing or provenance-marked background notifications. It preserves the latest
observation per worker, including recovery from failed to done, across further
summaries. It never scans arbitrary report prose for an ID, never treats an
assistant promise as a completed operation, and never promises that a historic
worker is still live or resumable.

Previously saved worker facts are imported only from the host-fact suffix after
a valid exact user-context carry. Lookalike metadata in model prose or a known
human message is not promoted. The catalog shares the existing 2 KiB fact
budget and 20-entry list bound; recent observations take priority. This is a
bounded continuation aid, not a replacement for the full archived transcript.

Regression coverage includes 20 repeated carries, latest-status recovery,
pruned metadata, async provenance, forged prose, summary-body lookalikes,
immutability and output-size bounds. Transport tests instantiate the Chat,
Responses and Anthropic request builders and confirm that swapping the tested
prompt changes no other provider request field, including reasoning/budgets.

## Reproduction and data

Normal tests skip all live/real-data runs. Explicit experiment variables:

```
SUPERCLI_SHORT_SUMMARY_LIVE=1
SUPERCLI_SHORT_SUMMARY_STAGE=<separate portable output folder>
SUPERCLI_SHORT_SUMMARY_FIXTURE=<isolated long-session fixture folder>
```

Tests: `TestShortSummaryTimingLive`, `TestShortSummaryRepeatedRetentionLive`,
`TestShortSummaryCandidateSmokeLive`. The first writes seed summaries needed by
the retention test. Original fixture data must remain separate from application
data. The read-only token source refuses to rotate the original OAuth login.

Private fixture/report files remain under ignored `.tmp/summary-short-*` folders.
Public timing logs contain counts/timings rather than prompts, private response
text, encrypted reasoning or credentials. No original transcript was rewritten.

## Final validation and builds

After removing the tentative short-default wiring, the retained worker-fact
change passed:

- `go test ./internal/agent/... ./internal/llm ./internal/storage/session ./internal/storage/memory -count=1`
- `go vet` for the same packages;
- `go build ./...`;
- targeted worker/transport tests, including failed `send_message` results in
  direct and `invoke_tool` envelopes;
- fresh CLI `--version` and hidden GUI `-h` smoke checks.

The explicitly opted-in candidate readiness smoke failure remains a recorded
failed experiment; it is not described as part of the passing regression suite.
Standard tests skip all paid/live fixture tests without explicit opt-in.

Fresh binaries with the retained accuracy fix are `supercli-context-safety.exe`
and `supercli-web-context-safety.exe`, beside the original application binaries.
The original running/main executables were not replaced. Temporary binaries
containing the rejected short-default experiment were moved into the ignored
experiment stage, away from the application launch directory.

## Later follow-up

The later [readiness follow-up](2026-10-19-readiness-summary-followup.md) fixes a
reproducible host-fact retention defect, corrects the live process fixture and
records a fresh six-round comparison. After that comparison and a direct live
production smoke passed, the evaluated short state prompt was enabled for the
shared auto-summarizer. The rejected rollout and failure above remain the
historical results of this earlier experiment.
