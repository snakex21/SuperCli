# GUI cadence recovery after a provider pause

Date: 2026-10-02. Integrated baseline: merge 24f0147 (our 4614dc1 and remote 9b9b43f). User reports GUI with OpenCode Zen Space Bunny; TUI was independently checked. This change only refines client display pacing. It does not change prompts, provider transport, the special Zen path, token usage, timers, dependencies, options or application storage.

## Problem and change

The GUI displayed resumed packets using the maximum of the last six arrival gaps. A one-second pause became a remembered 600-ms sample, so newly received text could still take about 320 ms to finish displaying even after subsequent packets resumed every 20 ms.

An arrival gap beyond 600 ms now clears that existing cadence history and resumes with the existing short default buffer. Gaps at or below 600 ms keep their previous behavior. The first received fragment still paints on the next rAF; the complete original source remains authoritative. First content, new reasoning/answer sections, tool sealing and completion keep their immediate/full-flush behavior. Large or expensive formatting still bypasses animation.

This removes an artificial slowdown after receipt. It cannot eliminate an upstream interval during which no text arrives.

## Deterministic replay

The portable .tmp/cloud-ui-cadence-2026-10-02 fixtures evaluate the production scheduler with a fake clock and a paint sink. They use 16-ms frames, generated ASCII packets and no model, browser, DOM layout, network or wall-clock timers. A saved merged baseline permits exact reruns after porting.

| Scenario | Full received-packet display before | After |
| --- | ---: | ---: |
| Start., one 1000-ms gap, 160-byte packet, frames relative to arrival | 320 ms | 48 ms |
| Three fast packets then 1000-ms gap and one packet, global frame grid | 332 ms | 60 ms |
| Five fast packets, 1000-ms gap, ten packets every 20 ms | Up to 332 ms | Up to 40 ms |
| 280/250/300/330-ms ordinary irregular cadence | Up to 326 ms | Identical |
| New answer after queued reasoning | Immediate | Identical |

The first resumed partial text is already shown on the next frame in both versions. These numbers measure draining the complete received packet, not provider TTFT, time to first character, physical FPS, WebView2 RAM or inference speed. The roughly 976-ms visible gap caused by the simulated upstream silence remains; no unreceived characters are invented.

Fast/pause/fast replay uses 2400 received ASCII bytes and reduces paints from 35 to 22 by shortening the animation of existing bytes. Complete displayed parts and raw source remain identical; pending queues drain. These paint counts are deterministic fixture work, not measured native render cost.

Artifacts: REPORT.md, resume-minimal.cjs/result JSON, cadence-replay.cjs, cadence-results.json and test outputs in the portable directory above. The fuller report separates old native WebView2 profiles from current fake-clock evidence.

## Verification

Three new tests extend stream-first-paint.test.cjs:

- Resumed prose and reasoning finish within four 16-ms frames after a long silence, with exact raw source, intact Unicode and completion cleanup.
- The 600/601-ms boundary retains ordinary smoothing and restarts only beyond the existing sampling horizon.
- Ten rapidly resumed packets drain promptly without losing text or retaining the old queue.

Existing tests retain ordinary Space Bunny cadence, 60/high-refresh work budgets, sparse characters, reasoning-to-answer stability, large/expensive bypass, Unicode, detached/replacement recovery, scroll geometry/manual follow mode and incremental Markdown identity/parity. The merged remote media/history paging changes are outside this pacing branch.

Focused JavaScript result: 41 PASS. A baseline read overlay removes only this pacing change; the three new tests fail on that prior policy while the existing tests pass. The full merged JavaScript runner passes all 154 tests; output is ported-full-ui-tests.txt.

Three existing TUI tests for dense bursts, sparse/first sections and tool/done/error flushing pass. TUI has no learned 320-ms smoothing backlog; its dense formatting coalescing uses an existing roughly 8-ms preparation window plus 16-ms frame tick. Those are scheduling rules, not native pixel-delay guarantees. No TUI source change was made.

No live provider or native GUI test was performed for this small port. Whole-program Go checks, release builds and source guards belong to the integrating agent.
