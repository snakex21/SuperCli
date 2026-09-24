# Avoid redundant terminal streaming work (2026-09-23)

## Problem and change

The TUI appended each incoming message/reasoning chunk to two growing strings,
then assigned one string over the other during refresh. A separate 16 ms tick
also repeated Markdown rendering and viewport layout even when neither the
text nor the inline spinner frame had changed.

A single growable buffer now supplies immutable text snapshots to the model and
chat. It is held through a pointer because Bubble Tea copies Model values.
Advancing an older copied Model detaches from a newer buffer; resets detach the
buffer without overwriting archived strings. Completion/error and new-run paths
clear both current views.

Timer updates compare the current text, completed-history dirty state and inline
spinner with the last painted state. Unchanged frames skip Markdown/layout.
New text is still rendered immediately, with the same event/timer cadence.
Changed spinner frames, new tool/notice rows, resize and explicit view changes
still refresh. Scroll-follow behavior is retained.

Scope: the terminal UI. GUI rendering, provider requests, model prompts, token
budgets and OpenCode Zen transport are unchanged. This adds no model work or
persistent cache. New content still receives a complete Markdown render; the
patch does not claim to make all rendering incremental.

## Paired measurement

Windows amd64, Ryzen 7 5800X3D, no-color TUI at 100 x 36 cells. Independent
before/after test executables used the same fixture. The baseline was built
through a Go overlay containing the original three production files, with the
new helper and its regression tests excluded only from that baseline build.
The workspace itself remained on the new code.

Sequential order: before/after, after/before, before/after. Medians of three
runs are shown. Unchanged-frame tests used 100 iterations per run; complete
stream-event fixtures used 10 iterations.

| Work | Before | After |
|---|---:|---:|
| Unchanged 4 KiB reply: one timer update | 0.459 ms | 0.037 ms |
| Unchanged 32 KiB reply: one timer update | 3.308 ms | 0.026 ms |
| 180 text fragments + 180 unchanged-frame updates | 106.563 ms | 57.404 ms |

The complete event fixture concatenates a 1,860-byte Markdown reply from 180
chunks. Each text event is followed by a timer event with no further content or
spinner change. Allocated bytes for this fixture fell from 50,687,422 to
46,092,915 (about 9%); allocation count fell from 243,591 to 123,207 (about 49%).
Its elapsed handler time fell about 46%.

The 32 KiB unchanged-frame allocation count fell from 11,169 to 7, and allocated
bytes from 906,624 to 114,785. Model-value copies and other UI overhead remain;
the fast path is not allocation-free.

These tests measure synchronous Model.Update handling, Markdown rendering and
viewport content/layout. They do not execute returned timer/read commands, wait
for real network tokens, run the full Bubble Tea event loop or measure physical
terminal drawing. Actual savings depend on chunk cadence, output length,
spinner changes, formatting and terminal. They are not model-generation or GUI
latency improvements.

## Validation

Existing live-view tests still see every text fragment before DoneEvent.
Additional regressions cover:

- Polish, Chinese and emoji text, copied model branches and retained snapshots;
- replacement/seeded current text and reset isolation;
- unchanged frames, dirty history and actual spinner-frame changes;
- scroll position during streaming and resizing;
- mixed reasoning/content, DoneEvent, errors, restart and buffer release.

Full go test ./... and go vet ./... passed.

    go test ./internal/ui/tui
    go test ./internal/ui/tui -run '^$' -bench '^BenchmarkTUIUnchangedStreamFrame$' -benchtime=100x -count=3
    go test ./internal/ui/tui -run '^$' -bench '^BenchmarkTUIStreamEvents$' -benchtime=10x -count=3

Raw app-local evidence: .tmp/tui-stream/paired.json, before-sources.json,
before-overlay.json, before-bench.json, after-bench.json, test-all.json and
vet-all.json. The two benchmark executables are retained alongside the results.
