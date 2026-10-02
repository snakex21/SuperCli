# Live GUI input residency — 2026-10-03

Base: 306ff56 (dev.13). After a live tool input was expanded, addToolCall still retained its original JSON through row._toolArgs and the lexical context shared with the permanent body-visibility toggle listener. The consumed renderer was already released by the previous fix; this was a separate remaining owner. Production code does not read _toolArgs.

The input callback now clears both references only after successfully appending the complete formatted input. Folded and stopped inputs stay available until opened. Formatting or insertion errors keep the original arguments and preserve the existing error and consumed-once behavior. Input/output order, body visibility, reopening identity, task/continuation briefs, readonly inputs and saved-history rendering remain unchanged. There is no new timer, cache, provider call, persistence change or transcript truncation.

## Ownership evidence and rejected shortcut

An isolated dependency-free fixture executes the actual production addToolCall/addToolResult path with generated create_file inputs. After expansion and explicit GC, a V8 heap snapshot identifies a 157,944 B raw JSON string retained by Context.args. That context is retained by an anonymous callback in the event-listener Set. Removing only _toolArgs leaves this path and its payload alive: with twenty generated inputs, heapUsed changes from 13,045,288 to 13,045,120 B, which provides no meaningful payload release. That shortcut was rejected.

## Generated retained-heap comparison

| Generated expanded inputs | Original JSON bytes | Baseline V8 heap | Final overlay V8 heap | Observed difference | DOM nodes before / after |
|---:|---:|---:|---:|---:|---:|
| 8 × 80 generated lines | 32,864 | 6,054,080 B | 6,021,176 B | 32,904 B | 145 / 145 |
| 20 × 3,000 generated lines | 3,188,570 | 13,045,288 B | 9,849,992 B | 3,195,296 B | 361 / 361 |

Every complete input hash matches. Input nodes and output order survive completion and reopening. The production port is byte-identical to the measured two-line overlay. Measurements use Node v24.15.0, a DOM double and explicit GC; allocator/GC variation and fixture objects are included. Input sizes are generated shapes, not a sample of ordinary saved sessions. These figures establish V8 payload ownership, not native WebView2 RSS, FPS, input latency, provider throughput or token savings. The complete formatted DOM remains retained and visible.

## Verification and scope

Three added regressions cover complete JSON/Unicode/malformed/empty input, reopening, formatting and insertion failures, and source reachability while the permanent visibility listener remains alive. The WeakRef check boxes only its source for observation; ordinary string payload behavior is covered by the existing production call/result tests and hash fixture. On the baseline, the release and reachability tests fail; the exception-control test passes. All 170 frontend tests pass on the final source (3,220.0 ms), including no-history/no-image streams, reasoning transitions, scrolling, paging, task rows and media preview paths.

Media-dialog cleanup already unloads its sources and removes children; site-preview destruction releases its controller/frame and disconnects its observer. No new retained object URL or closed-preview leak was established in this bounded audit. R8 raw-page release, R9 worker placement and R10 consumed-renderer release were not repeated or changed.

Portable ignored evidence: .tmp/goal-ui-round11-2026-10-03/REPORT.md, production source snapshots and isolated overlays, live-input-retention.cjs and before/after receipts, heap snapshots/retainer graphs, baseline-regression.json, final-ui-tests.json and frozen source hashes.
