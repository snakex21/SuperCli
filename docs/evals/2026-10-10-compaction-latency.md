# Compaction latency and instruction-echo protection — 2026-10-10

The user's model and thinking selection are unchanged. Two instruction changes
were tested on the actual local Qwen and rejected: neither made the compactor
faster. The resulting production change protects conversation state when a
model returns the empty instruction template instead of a summary.

## Controlled comparison

Model: `qwen3.8-27b-uncensored`; thinking enabled, configured effort `max`, generation limit
8192. The GUI comparison recorded configuration and provider messages, not every
HTTP retry body; it does not establish that the backend accepted `max`. See the
subsequent explicit HTTP validation below. All runs were sequential, without parallel inference or heavy build
work. Completion was awaited rather than polled.

The frozen fixture contains a previously completed, real Node coding task with
observed failed tests, a pricing patch and passing tests, plus user maintenance
instructions and two neutral recent turns. The actual GUI loop compacts thirteen
older messages and makes a normal subsequent main request. The raw retained tail
cannot supply the coding facts or earlier constraints being tested.

Fixture SHA-256:
`e42dfd29a02143df4551e8ea2b6eb728394abceb3dd0170cd273b0bb7b75180f`.
Compactor transcript SHA-256:
`0a1034fa90d77bab16de8b01b93dca8f3ab2c9a0a2d3c040c3b53cc8db896307`.
The model, selected thinking, transcript, main system prompt, native definitions
and fixture hash match across the four paired runs. Each run made two logical
provider calls and executed zero tools. Their internal HTTP retry count was not
captured by this comparison harness.

The first rejected candidate required four terse lines and one occurrence of
each fact. It retained the nine tested facts in both summary and continuation,
but its compactor took **66.342 s** with 1762 reasoning tokens. Its preceding
original-prompt run took 24.940 s but omitted the no-publishing requirement.

The second candidate used a simpler shortened instruction and omitted routine
read inventories unless needed or explicitly requested. It retained the same
requirements in read-only review. The A–B–B–A result was:

| Run | Compactor | Following main request | Combined | Summary / continuation checks |
|---|---:|---:|---:|---|
| Original A1 | 39.007 s | 33.859 s | 72.867 s | 9/9, 9/9 |
| Candidate B1 | 92.029 s | 28.044 s | 120.074 s | 9/9, 9/9 |
| Candidate B2 | 107.054 s | 25.197 s | 132.254 s | 9/9, 9/9 |
| Original A2 | 38.414 s | 166.550 s | 204.966 s | 1/9, 3/9 |

Median compactor time increased from **38.711 to 99.542 s**. Its input decreased
only from 8180 to 8169 tokens; median reasoning increased from 1060.5 to 2569
tokens. The new instruction also produced larger summaries. Two samples per arm
do not establish general performance, but provide no reason to deploy this
candidate. Aggregate combined time would obscure A2's invalid-summary failure;
it is not evidence of a candidate speedup.

Automatic checks are bounded lexical evidence; full summaries and continuations
were inspected for the cause, zero/default behavior, observed test progression,
pending documentation, protected tests/catalog, dependency restrictions,
portable data and future no-publishing requirement. Neither a nine-check pass
nor these samples guarantees lossless arbitrary model summaries. Missing cache
accounting is unknown; zero-filled usage is not evidence of disabled caching.

Both experimental prompts are retained in the ignored portable staging folder
`.tmp/compaction-latency-fix/`, together with the original source, binaries,
hashes, sequential logs and `comparison.json`. The real GUI receipts remain
under `.tmp/coding-efficiency-fix/compaction-runs/`:

- A1: `run-2223862850/receipt.json`
- B1: `run-3519779938/receipt.json`
- B2: `run-2046876869/receipt.json`
- A2: `run-332108795/receipt.json`
- First candidate: `run-3094470409/receipt.json`
- Its original-prompt comparison: `run-2818158336/receipt.json`

## Implemented protection

A2 returned the four literal placeholder lines from the summarization
instruction. The previous code accepted the completed, nonempty response and
replaced conversation facts with those placeholders. The following main request
then lacked the cause, observed test results, pending documentation and most
earlier constraints; its answer took 166.550 seconds.

The compactor now rejects a response equal to its empty template or its complete
instruction. Only blank lines, CRLF and outer line whitespace are normalized.
Meaningful summaries, other section formats, and quotes accompanied by actual
facts continue to work. This is deliberately a precise validity check, not a
semantic judge or another inference call. Paraphrased empty templates and other
model omissions are outside its protection.

Manual compaction retains its history and reports the error. Threshold and
context-limit compaction propagate this specific error before the ordinary
hide-history fallback; the running turn ends without a further main request or
context-overflow retry. The model-switch path already preserves full history
after a failed helper and can continue with that full history.

The existing configured side-model fallback remains bounded. If its template
echo is followed by an ordinary active-model failure, both error causes remain
available to `errors.Is`, preventing the error from becoming a blind hide.

The production instruction remains **byte-identical at 870 bytes**. The limit,
provider routing, reasoning selection, transcript, exact-file-fact appending,
recent-turn protection, cancellation and stream-usage handling remain in place.
No app data is saved outside the application/repository folder.

This protection avoids downstream work on a known empty memory. It does not
claim to accelerate valid model generation. The observed 166.550-second main
request is an example of the failure cost, not a promised general saving.

## Verification

Regression tests exercise literal and full-instruction echoes, whitespace and
reasoning wrappers, meaningful quotations, manual/automatic/emergency history
preservation, stopping before another main inference, preventing a context-limit
retry, and preservation of the error through a failed side-model fallback.

An additional opt-in quality harness uses two explicitly **synthetic** histories
to exercise unfinished commands, worker IDs, promises versus completion,
Unicode paths, independent tasks, open program-export errors and revoked user
requirements. Only its summarizer calls are real; the historical tools and
workers do not run. It preserves full outputs and actual HTTP requests, verifies
the requested model/max controls, and requires manual semantic review. These
quality probes are not latency benchmarks or real coding/export executions.

The additional live quality probes did **not** produce valid model-quality
results. Explicit validation captured this actual loopback response:

```text
HTTP 400
Invalid 'reasoning_effort' value: 'max'.
Supported values: none, minimal, low, medium, high, xhigh.
```

The existing provider adapter then attempted a compatibility retry without the
effort field. The quality harness rejected that changed request before sending
it, preserving the requirement to avoid a different thinking setting in these
probes. No quality pass or backend acceptance of `max` is claimed. The diagnostic
receipt is `.tmp/compaction-latency-fix/quality/run-3165830053/failure_unfinished_workers.json`;
the earlier incomplete probe receipts and logs are retained as failed diagnostics.
The backend rejection also limits interpretation of the GUI comparison: user
configuration stayed identical, but accepted wire-level effort was not measured.
The production reasoning adapter and the user's settings were not changed.

The offline evidence validators and actual-wire replay passed. They reject
changed effort, contradictory synthetic task state, and misleading matches in
unrelated clauses. Replaying a completed failed receipt verified its original
`max` request while retaining the rejection of its omitted-effort retry.

Validation of the production change completed successfully:

- `go test ./internal/agent -count=1` (including manual, threshold, context-limit
  and configured side-model echo regressions).
- `go test ./internal/app ./internal/ui/tui ./internal/webgui -count=1 -timeout=10m`.
- `go vet ./internal/agent ./internal/app ./internal/ui/tui ./internal/webgui`;
  agent vet and focused harness tests repeated after diagnostic-only test edits.
- Both command binaries built successfully. The original instruction hash
  matches the actual current request: `e3358531d259789833ab1334a07930f9c3d874d4ebca52e0d5c8b4c71075f663`.

Build/check receipts and full logs are in `.tmp/compaction-latency-fix/`.

Both binaries were installed with verified previous-version backups under
`.tmp/compaction-latency-fix/backups/a43e5364-47ea-4f97-8350-682882e931d0/`.
Installed hashes match the staged builds; both installed `--help` smoke checks
returned zero. `installed.json` and `installed-smoke.json` retain the evidence.
The existing running application was not restarted; reopening CLI/GUI loads the
new compaction guard.
