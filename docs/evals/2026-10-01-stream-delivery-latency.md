# GUI and TUI stream delivery latency

Date: 2026-10-01

## Findings and changes

The GUI server held every subsequent text/reasoning packet for a fresh 40 ms batch window, even when the previous packet had been emitted much earlier. The server now batches only dense consecutive text bursts within 8 ms of the previous emission. A sparse packet outside that window is emitted immediately. The pending timer uses the remaining deadline rather than restarting a full window. Initial section text, semantic boundaries and large bursts remain immediate. Text-free reasoning-token notifications are also preserved; the previous text-only filter dropped them.

The frontend's measured frame scheduling and bounded pacing are unchanged. The provider adapters, agent context, Zen routing and number of model requests are unchanged. Dense streams may produce more SSE writes (bounded by the short consecutive-text window); sparse streams keep the same write count.

The TUI formatted/wrapped the entire growing assistant message for every provider fragment before requesting the next event. Large rapid streams could therefore queue behind presentation work. It now accumulates every fragment immediately and limits formatting of dense messages above 1 KiB to an 8 ms preparation interval. The existing 16 ms frame tick paints any pending tail. Short answers, sparse packets and first text after a reasoning/tool boundary still prepare immediately. Tool calls/results, completion and errors flush the full pending text immediately. No new timers, settings, model prompts or dependencies were added.

## Controlled measurements

The GUI probe used the actual Engine.runStream path with a timed synthetic provider, measuring from provider send to the emitted wire event. It sent 40 packets per case. Browser display and external provider generation are outside this measurement.

| GUI delivery workload | Before | After |
| --- | ---: | ---: |
| 50 ms packets: p50 / p95 added delay | 40.5 / 41.0 ms | below 0.1 / below 0.1 ms |
| 2 ms packets: p50 / p95 added delay | 18.3 / 36.3 ms | 4.0 / 8.2 ms |
| 2 ms packets: emitted text frames | 3 | 11 |
| 50 ms packets: emitted text frames | 40 | 40 |

The TUI probe ran its real Bubble Tea program and standard renderer against an instrumented output writer. A timed producer sent 500 Markdown-containing lines at 2 ms intervals (roughly 35 KiB), using a buffered event channel. Delays were measured from production to each marker's first terminal output. Physical terminal display/compositor behavior is outside the writer measurement.

| TUI stream measurement | Before | After |
| --- | ---: | ---: |
| p50 delay | 15.9 ms | 15.0 ms |
| p95 delay | 221.2 ms | 24.8 ms |
| Maximum delay | 271.7 ms | 31.9 ms |
| Run duration | 1.27 s | 1.01 s |
| Terminal bytes written | 205663 | 164260 |

All 500 markers reached the renderer and the completed assistant text matched the produced text exactly. An intermediate 16 ms preparation interval reduced p95 but increased median delay; the selected 8 ms interval plus the short-answer bypass avoided that tradeoff in this fixture. These are single-workload comparisons, not universal response-time guarantees.

## Validation

- Full Go tests passed for internal/webgui, internal/ui/tui and internal/app.
- All 67 UI tests passed. No frontend assets were changed in this follow-up.
- New deterministic coalescer tests cover sparse packets, deadlines, Unicode, burst limits, semantic ordering and text-free reasoning counts.
- New TUI tests cover immediate first/sparse sections, full accumulation between frames, and terminal/tool flushing. Existing scroll preservation, copy/snapshot independence, reasoning separation, queue/session recovery and short live-view tests passed.
- Native/probe source and pre-change source snapshots are stored locally under .tmp/gui-tui-stream-delivery-2026-10-01; probe test files were removed after completion.

Windows GUI and TUI executables were rebuilt at the existing 1.0.1 version.
