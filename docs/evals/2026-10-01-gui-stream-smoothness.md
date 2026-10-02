# GUI stream rendering and provider packet cadence

Reported behavior: streamed GUI text appeared in abrupt updates. The frontend waited 40/60/120/250 ms based on the accumulated raw message length, in addition to the server's existing 40 ms SSE write coalescing. A long reasoning block could therefore keep delaying the short visible answer at 250 ms. Each paint also replaced the complete message DOM.

The initial fix coalesced only events in the same animation frame. First content remains immediate, and terminal/tool boundaries synchronously drain and cancel the pending frame. Existing text nodes, completed paragraphs, tables/code and reasoning elements are reconciled in place; growing text appends to its node when possible. User-folded reasoning stays folded without rebuilding its element. Unchanged final flushes skip parsing and DOM work. Markdown/DOM failures still fall back to the exact raw text and recover on the next update. Provider requests, backend SSE batching and context paths are unchanged.

## Native WebView2 comparison

A before/after pair used the actual embedded frontend and the same native Go/WebView2 fixture with fresh portable data/profiles. It sent 60 content events 40 ms apart, first for short prose and then after a 54000-character reasoning block. This is a synthetic arrival pattern, not provider generation speed.

| Measurement | Before | After |
| --- | ---: | ---: |
| Short-answer paints for 60 events | 60 | 60 |
| Short-answer arrival-to-DOM latency, p95 | 48.1 ms | 6.2 ms |
| Visible-answer paints after long reasoning | 10 | 60 |
| Long-reasoning case arrival-to-DOM latency, p95 | 255.5 ms | 0.9 ms |
| Long-reasoning case interval between DOM updates, p95 | 296.7 ms | 54.2 ms |
| Root element replacements, short/long cases | 60/10 | 0/0 |
| User-folded reasoning element identity retained | no | yes |
| Render duration after long reasoning, p95 | 0.7 ms | 0.6 ms |

Timings measure DOM updates, not a compositor presentation guarantee, and vary with runtime scheduling/hardware. Both native runs preserved the exact final stream text and folded state, matched full Markdown output at 73 partial prefixes (headings, bold/italic, links, quotes, lists, table and code fence), and recovered after an intentional render failure. No uncaught JavaScript errors were captured. Repeating an unchanged final render retained the existing elements after the change.

All 48 UI tests passed, including queue/Stop recovery, history paging, lazy viewers, locales, same-frame coalescing, long-reasoning updates and terminal/tool-boundary draining. Full internal/webgui Go tests passed. Native fixture source and raw before/after reports are local under .tmp/gui-stream-2026-10-01. Windows executables were rebuilt from the validated assets.


## Follow-up: bursts still visible with OpenCode Zen

The user still saw stutters after the first fix. A real request through the existing Factory/Zen transport, using the configured space-bunny-free model, produced 15 visible reasoning/content fragments over 4.77 s; gaps reached 330.8 ms. The isolated probe used app-adjacent data and did not modify the user's conversation, configuration or the Zen transport. Its generated sample text/timing trace is local under .tmp/gui-zen-stream-2026-10-01. A frame-based renderer cannot display characters the provider has not sent.

The follow-up change separates the complete raw transcript from the displayed prefix. Small prose packets are revealed over a few animation frames, using observed arrival cadence and a deadline of at most 120 ms per packet (plus the next eligible paint). First content is immediate; native reasoning-to-answer boundaries, tools, completion, errors and Stop drain immediately. Same-frame packets merge, while overlapping packets keep their own deadlines. Large backlogs (2048 UTF-16 code units) and formatting that took over 6 ms on the preceding paint bypass animation. Animated paints are limited to approximately 60 Hz, including on faster displays. UTF-16 pairs and complete reasoning protocol tags are kept intact. No model request, instruction, provider route or server batching changed.

The actual embedded GUI was tested in native WebView2 with the recorded Zen arrivals, then with 80 older transcript blocks, 200k characters of folded reasoning and an 1800-row Markdown table. Baseline here is the preceding frame/DOM-retention fix, not the older timed renderer.

| Recorded Zen replay | Frame-only baseline | Bounded reveal |
| --- | ---: | ---: |
| Visible DOM updates | 14 | 80 |
| Interval between DOM updates, p95 | 337.2 ms | 206.5 ms |
| Added raw code units per update, p95 (including reasoning/tag transitions) | 161 | 30 |
| Arrival-to-complete-packet display, maximum | 7.7 ms | 120.9 ms |
| Animation-frame intervals above 34 ms | 0 | 0 |
| Exact final raw text | yes | yes |

Across the synthetic short/long-reasoning cases, packet-display latency reached 134.7 ms including scheduling and the eligible 16 ms paint slot. The costly-table fallback retained 48 updates for 48 packets; render p95 was 13.0 ms versus 13.2 ms before, with no animation-frame interval above 34 ms. All four scenarios preserved the final formatted DOM and captured no uncaught errors or long tasks. These are DOM/animation-frame measurements, not compositor presentation guarantees or proof that all user configurations are covered. Provider gaps remain visible; removing them completely would require a larger display buffer. The smoothing is a bounded presentation tradeoff, not a faster model or network.

Permanent UI regression tests cover immediate first content, progressive packet display, per-packet deadlines, long-reasoning boundaries, terminal/tool draining, expensive-formatting fallback, Unicode/tag boundaries, high refresh rates, suspended frames and transcript correction. Native fixture and before/after reports are local under .tmp/gui-stream-frames-2026-10-01.


Final verification: all 53 UI tests and the complete internal/webgui Go package passed. Both Windows executables were rebuilt as version 1.0.1, backed up under the fixture directory and replaced locally; SHA-256 equality with the validated candidates was verified. No commit, push or release was made.


## Integrated incremental renderer and both visible channels

The user reported that reasoning arrived in layers too. The preceding 120 ms
prose-only reveal deliberately bypassed native reasoning, and its short display
window could still drain before the next 250-330 ms Zen packet. Commit 2b746f0
was fetched and fast-forwarded from origin/main. Its incremental Markdown/code
renderer and lazy historical thinking were integrated while retaining the local
lazy tool viewers, locales, model picker and preflight/startup changes.

A pure integration was checked first: unchanged huge-table append work fell from
13.1 ms to 0.3 ms at p95, but source-paced Zen updates still had roughly 331 ms
gaps. Renderer efficiency and packet presentation are separate measurements.
The final shared GUI scheduler therefore paces reasoning and prose independently
using already-received section text. A new answer section appears immediately,
including when the final thought is still being revealed. Recent gaps (last six
positive samples) choose a small window, capped at 320 ms plus an eligible paint.
Typical fast 40 ms packet streams need about 48 ms, not the maximum window.
Each section keeps its own ordered deadlines; no cumulative typing backlog is
introduced. Tool boundaries, Stop, errors and completion synchronously cancel and
drain every section. Large backlogs or expensive formatting bypass the reveal.
Parts exclude protocol tags before pacing, UTF-16 pairs stay intact, detached
messages stop scheduling, and suspended frames catch up on resuming. No provider
request, Zen route, prompt instruction or model token count was changed.

Actual embedded assets ran in native WebView2 with identical recorded Zen
arrivals and five workload scenarios. Baseline is the preceding local 120 ms
prose-only smoother; the candidate combines the incremental renderer and the
independent section queues.

| Native WebView2 measurement | Previous local build | Combined build |
| --- | ---: | ---: |
| Recorded Zen: prose update interval, p95 | 189.5 ms | 18.9 ms |
| Recorded Zen: added prose code units per update, p95 | 27 | 4 |
| Recorded Zen: thought updates for the same two packets | 2 | 17 |
| Thought after the initial source wait: maximum update interval | one whole final packet | 18.9 ms |
| Synthetic 48 reasoning packets: update interval, p95 | 143.9 ms | 19.4 ms |
| Synthetic short prose: update interval, p95 | 112.4 ms | 18.9 ms |
| After 200k folded thought: total rendering work | 190.0 ms | 8.8 ms |
| After 1800-row table: total rendering work | 748.5 ms | 16.8 ms |
| Recorded Zen: maximum complete-packet display lag | 126.4 ms | 331.3 ms |

All five scenarios retained exact final source-derived text and formatted DOM,
with no animation-frame interval above 34 ms and no uncaught JavaScript errors.
The synthetic maximum packet-display lag was 336.9 ms including paint scheduling.
A separate native test checked 590 character prefixes, recovery snapshots,
fold state, lazy history opening/reopening, tools and finalization. Historical
reasoning used 6 elements while folded and 4006 when expanded, with full content
restored and no duplicate rendering.

The initial source wait is not reduced by presentation buffering. The recorded
thought had only two provider packets, so its initial roughly 292 ms wait remained;
after the second packet arrived, it was distributed over frames rather than
appearing in one layer. Continuous behavior cannot be guaranteed across arbitrarily
long provider/network stalls. These measurements concern DOM updates and animation
callbacks, not a compositor presentation guarantee. Native timings vary with
scheduling/hardware. The increased mid-stream display lag is an explicit, bounded
tradeoff for smoother text; first section content and completion have no deliberate
extra wait. The natural completion flush can still reveal the remaining tail at
once.

The native fixtures/reports and preserved pre-integration source/patch are local
under .tmp/gui-stream-integration-2026-10-01. The first native regression attempt
timed out because its injected test sample contained a literal closing script
tag; HTML-safe fixture embedding fixed that test-host issue. The successful final
regression is saved separately and supersedes the timeout.

Final source validation: all 57 UI tests and the complete internal/webgui Go
package passed. Both Windows version 1.0.1 executables were rebuilt from these
assets. No new commit, push or release was made by this turn; the other agent's
existing commit was imported by fast-forward.

The previous local executables were preserved under the fixture directory; both new local executables were installed and verified byte-for-byte by SHA-256.

## High-refresh follow-up: removing the fixed 16 ms pacing cap

A short follow-up replaces only the animated paint interval. Cheap measured
formatting can now use each native requestAnimationFrame callback. The minimum
interval is eight times the most recent formatting cost, capped at the previous
16 ms interval for moderately expensive updates. This is a budget for synchronous
formatting work, not a limit or measurement of total browser/CPU/GPU utilization.
The 320 ms source-dependent display window, first-section behavior, exact raw
transcript, heavy-update bypass and synchronous terminal flush remain unchanged.
No provider transport, model prompt or extra request was added.

The same five scenarios were replayed sequentially in the native WebView2 fixture.
This comparison uses the combined incremental renderer with a fixed 16 ms guard
as its baseline, rather than the older pre-integration renderer above. Native
animation callbacks had a median interval of about 6.3 ms and p95 about 6.4 ms.

| Actual text changes | Fixed 16 ms median / p95 | Adaptive median / p95 |
| --- | ---: | ---: |
| Recorded Zen, reasoning | 18.8 / 291.5 ms | 6.3 / 7.1 ms |
| Recorded Zen, prose | 18.8 / 19.4 ms | 6.3 / 12.5 ms |
| Synthetic reasoning bursts | 18.7 / 19.0 ms | 6.3 / 6.4 ms |
| Synthetic short prose bursts | 18.8 / 18.9 ms | 6.3 / 7.2 ms |
| Prose after 200k folded thought | 18.8 / 18.9 ms | 6.3 / 6.3 ms |
| Prose after 1800-row table | 18.7 / 18.9 ms | 6.3 / 6.4 ms |

The recorded thought's first source-dependent wait still remains; its fixed-cap
p95 above includes that interval. More frequent paints add formatting work: the
recorded Zen replay used 33.9 ms across 3698.3 ms (previously 14.1 ms), and the large
table scenario used 41.0 ms across 2775.4 ms (previously 15.2 ms). Single-update
rendering p95 remained at most 0.2 ms. There were no animation callback gaps above
34 ms, final text and final formatted DOM matched in all scenarios, and no
uncaught JavaScript errors occurred. Maximum full-packet display lag was 325.1 ms.

Sub-10 ms is supported on a sufficiently fast display with cheap formatting and
received text available. It is not guaranteed for every actual text change:
recorded prose p95 was 12.5 ms because individual characters can run out between
frames. Nothing is generated or repainted just to fill that gap. A 60 Hz display
still has roughly 16.7 ms native frames. These are DOM/callback timings, not a
compositor presentation guarantee, a CPU benchmark, or provider/model speed.

The local preserved fixed-cap source, bench and JSON reports are under
.tmp/gui-stream-integration-2026-10-01/high-refresh. Tests cover actual received
text progress at 120, 144, 165 and 240 Hz, the native 60 Hz limit, formatting cost
budget, and sparse characters without redundant paints, alongside previous
ordering, Unicode, Stop and completion cases.

Final verification: all 60 UI tests and the internal/webgui Go package passed.
The Windows GUI 1.0.1 executable was rebuilt, installed locally and verified by
SHA-256, with its previous binary preserved in the high-refresh fixture folder.
The TUI has no asset changes in this follow-up and was left at its existing build.
No commit, push or release was made.

## Follow-up: growing table and list tails

The previous table benchmark appended prose after a completed 1800-row table.
It did not cover continuously extending a table before any blank-line boundary.
The remaining tail renderer rebuilt the entire table/list for each animated
source prefix, including each incomplete new list marker.

Completed table rows and list items now remain in the DOM. Only the open row is
formatted; newly completed rows are appended in a fragment. Incomplete markers
such as "- " use a small temporary block after the retained list until they become
an item. Mixed completed block types, replacement snapshots, fences and empty
comment recovery still fall back to the authoritative renderer. Full rendering
and streaming share the same table-cell escaping, alignment and inline rules.

A separate native WebView2 replay used a 500-row table and 500-item list, followed
by 24 new packets at 40 ms intervals, alongside a long unfinished paragraph and
open code block. The baseline is the adaptive high-refresh build from the prior
follow-up. The fixture recorded actual formatting work, forced scroll-height
reads and native animation callback intervals.

| Scenario | Previous rendering p95 | New rendering p95 | Previous / new total rendering |
| --- | ---: | ---: | ---: |
| Growing table | 4.7 ms | 0.2 ms | 276.3 / 12.9 ms |
| Growing list | 2.5 ms | 0.1 ms | 142.2 / 10.9 ms |
| Long paragraph | 2.1 ms | 2.1 ms | 111.5 / 108.2 ms |
| Open code block | 0.6 ms | 0.6 ms | 63.9 / 58.2 ms |

The table's largest native callback interval decreased from 24.9 to 7.3 ms; the
list decreased from 12.6 to 7.3 ms. All four scenarios preserved exact final
formatted DOM, had no uncaught JavaScript errors and no observed long tasks.
These are one before/after replay on this machine, not universal compositor,
CPU or provider guarantees. The unchanged paragraph remains a separate cost.
Network gaps and waiting for new source characters are not removed by this change.

The native prefix regression now checks 929 partial-source boundaries against
the original renderer, including row retention across incomplete markers, mixed
block transitions, HTML parsing, recovery, fold state, lazy history, tools and
finalization. The dependency-free UI gate passed all 63 tests. Local preserved
source, scripts and native reports are in .tmp/gui-growing-tail-2026-10-01.

The fast paths also skip inert leading blank lines, including the newline emitted
after native reasoning closes, without changing the source transcript. A final
UI test covers this common thought-to-answer transition; all 63 UI tests pass.

A final replay of the previously recorded Zen trace preserved exact final text
and formatted DOM. Median prose updates remain 6.3 ms (p95 12.5 ms, with sparse
characters still limiting some frames); total rendering work for that replay
was 16.6 ms, compared with 33.9 ms in the preceding high-refresh report. All five
cadence/history scenarios passed with no uncaught errors and no native callback
interval above 34 ms. The Go GUI package passed and the GUI was rebuilt from the
final assets. No provider request or production prompt changed.

The previous local GUI binary was preserved in the fixture folder. The new
GUI 1.0.1 executable was installed locally and its SHA-256 matched the final
built candidate. No commit, push or release was made.
