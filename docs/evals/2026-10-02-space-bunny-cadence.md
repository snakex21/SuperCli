# Space Bunny cloud cadence diagnostic — 2026-10-02

## Result

This single bounded response reproduced irregular delivery before the GUI: Space Bunny text arrived in 14 packets separated by 65.683–862.150 ms. The median interval was 281.747 ms; chunks contained 16–138 Unicode characters (median 102). This is variable arrival cadence and intermittent catch-up, not evidence of a total stalled run.

The largest inter-text interval exactly matched a blocking response-body read: Read began at 3222.414 ms and returned at 4084.564 ms, and its text delta was observed at 4084.564 ms. The following text arrived 65.683 ms later. The long interval therefore already existed upstream of the local parsing/delivery consumer, within the provider/relay/network path. This measurement cannot distinguish model scheduling, provider batching, relay buffering and network conditions from one another.

## Scope and safeguards

- Exactly one HTTP attempt and one wire request, HTTP 200, model `space-bunny-free`, using actual `NewOpencode` and its unchanged public Zen authentication/header/endpoint path.
- Package-local temporary Go test overlay wrapped the existing HTTP transport and Body.Read only to record monotonic timestamps and byte counts. No production source or staged document was changed.
- Two synthetic messages, no tools, no user/project/session state, max 300 output tokens, process-local thinking disabled and effort `none`. No model change or configuration write.
- 60-second parent context; a transport guard prevents a second wire attempt. Non-2xx responses and any retry notice cancel the parent context. No requests were sent to Kilo, no catalog requests were repeated, and no live request was repeated.
- Logs contain timings, sizes and usage only, no generated prose/reasoning or credentials.

## Measurements

| Measurement | Observed |
| --- | ---: |
| HTTP response headers | 1435.303 ms |
| First text delta consumed | 1441.166 ms |
| Complete channel | 5228.117 ms |
| Text / reasoning events | 14 / 0 |
| Body reads with bytes | 34 |
| Text interval median / p90 / maximum | 281.747 / 358.312 / 862.150 ms |
| Chunk characters minimum / median / maximum | 16 / 102 / 138 |
| Latest completed body read → consumed text median / maximum | 0 / 0.639 ms |
| Provider input / output / cached input | 473 / 300 / 138 tokens |
| Finish reason | length (intentional 300-token diagnostic cap) |

Zero sub-millisecond observations reflect measured clock resolution; they are not a universal zero-cost claim. The body-read-to-delta figure is a timestamp association, not instrumentation of each individual parser publish instruction. The consumer immediately recorded deltas and did no UI work.

The redacted per-event trace and Read start/end timestamps are in `timestamps.json`; distributions and associations are in `summary.json`. The longest gap spans text events 6→7, with a subsequent short event 7→8 demonstrating catch-up.

## GUI backend and TUI inspection

At merged baseline 24f0147, `internal/webgui/stream.go` coalesces dense consecutive text inside an 8 ms window measured since the previous emission. First events and semantic/channel boundaries emit immediately; sparse packets beyond that window also emit immediately. `stream_run.go` starts a pending timer once and does not reset it for every incoming fragment. `run_chat.go` writes and flushes each emitted SSE frame.

An offline temporary overlay test replayed the 14 recorded timestamps through the actual private `messageCoalescer.pushAt`. It passed: **14 immediate emits, zero buffered packets**. Every recorded inter-text gap exceeded the 8 ms window. This rules out that coalescer adding the measured pauses in this particular trace. It does not measure the full HTTP handler, browser reception, native GUI paint or long-transcript CPU load.

TUI `waitForEvent` waits directly on the event channel. `refreshStreamTranscript` refreshes first/small/sparse text immediately, limiting preparation during dense bursts to roughly 8 ms; `streamFlushCmd` schedules a 16 ms frame refresh. These intended timers are not second-scale waits. Actual terminal rendering costs/FPS were not measured here, so the inspection does not rule out heavy-transcript rendering contention.

The baseline frontend intentionally paced received text using the maximum of the last six capped arrival gaps, up to 320 ms plus a paint. This kept a slow outlier in the later display buffer. The integration now resets that existing history after a gap beyond 600 ms; see [GUI cadence recovery](2026-10-02-goal-cloud-cadence-recovery.md) for baseline/current deterministic replay and regression tests. This measurement itself did not change frontend or provider code. Changing backend coalescing would not remove the measured 862-ms source interval.

## Reproduction

All artifacts are under `.tmp/cloud-cadence-2026-10-02/`: `cadence_test.go`, `overlay.json`, `operation.json`, `timestamps.json`, `summary.json`, `replay_test.go`, `replay-overlay.json`, `replay-operation.json` and this report.

The live invocation was `go test -overlay .tmp/cloud-cadence-2026-10-02/overlay.json ./internal/llm -run '^TestLiveCadenceSpaceBunny$' -count=1 -timeout 90s -v`, with `SUPERCLI_CADENCE_REPORT` pointing to repo-local `timestamps.json`. It completed PASS. Do not rerun it merely to check progress or seek another timing sample.

Offline replay: `go test -overlay .tmp/cloud-cadence-2026-10-02/replay-overlay.json ./internal/webgui -run '^TestCadenceReplayKeepsSparsePacketsImmediate$' -count=1 -v`, with the same report path; PASS. All cache/temp paths used the existing repo-local evaluation environment.

This is one provider observation with an intentionally small context, not an A/B throughput benchmark or a measurement of native GUI/TUI FPS, RSS or user session behavior. No production bug is asserted from the temporary measurement setup.
