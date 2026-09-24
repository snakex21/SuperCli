# Optional consultation initialization on demand

## Evidence and change

Recent CLI logs show three sample providers (llama-3.1-8b, gemini-2.0-flash and gpt-4o-mini) being constructed on every TUI startup. The automatic council is optional, yet startup built it before the UI was ready. Provider construction can include catalog discovery or authentication reads; in the existing Zen resolver an unknown model can trigger catalog refresh even with a fresh cache.

The CLI now registers a council with a lazy default sample pool. Only an actual automatic consultation resolves it. The first successful resolution is shared across concurrent and subsequent calls using sync.OnceValue; an unavailable pool remains unavailable for this CLI run, as with the previous eager setup. Explicit model rosters bypass this fallback. The existing factory, sample selection and provider transport are unchanged.

The shared consultation engine resolves samples before clamping N. The workflow tool leaves implicit pool clamping to that engine, preserving n=1, the default of three, and an explicit MaxN. Invalid or already-canceled automatic consultations do not initialize the pool; cancellation during initialization is checked again before inference. Existing provider constructors are synchronous and cannot be interrupted mid-construction.

This removes work from CLI startup, not from a requested consultation: its first use now pays the construction cost. It does not reduce generated tokens or claim faster model inference. The GUI does not currently eagerly build this default council, so no GUI startup gain is claimed. No model prompt, new helper inference, provider-specific transport code or storage path was added.

## Validation

- Concurrent first use (eight consultations) constructs one pool of three providers, with exactly eight metered sample calls for n=1.
- Explicit roster bypass, invalid input, cancellation before/during resolution, unavailable providers and correct sample-count clamping.
- Full go test -timeout 90s ./... passed; targeted go vet passed.
- Existing provider and Zen transport tests passed as part of the full suite.

## Isolated microbenchmark

Windows / Ryzen 7 5800X3D; three samples, 200 ms per sample. The factory returns in-memory Echo providers; no network, model server or artificial delay. This measures only optional wiring and allocations, not whole application startup.

| Median per startup | Before (eager) | After (lazy) |
| --- | ---: | ---: |
| Optional provider constructions | 3 | 0 |
| Wiring time | 683.1 ns | 120.3 ns |
| Allocated bytes | 720 | 328 |
| Allocations | 16 | 4 |

The useful guarantee is zero optional provider construction on ordinary startup, including any discovery work those constructors would perform. The sub-microsecond benchmark itself is not evidence of a perceptible latency improvement. No live provider latency estimate is claimed.

Artifacts: .tmp/lazy-consult-startup/validation-0.json (suite), validation-1.json (benchmark), check-0.json and check-1.json (targeted checks).
