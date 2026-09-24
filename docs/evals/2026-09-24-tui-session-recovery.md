# TUI session continuity, draft recovery and clipboard screenshots — 2026-09-24

## Behavior

- TUI resume now switches the loop's transcript and session writer together. Further messages and attachment metadata append to the selected conversation. Per-call usage is captured against the ID at run start, so a late counter cannot land in a newly selected session.
- Legacy aggregate token counters are preserved once before detailed per-call accounting begins; they cannot disappear from totals or be counted twice.
- The GUI reads the same canonical session rows and model projection. Existing older split continuations are not automatically merged.
- Startup --resume uses the same TUI loader and restricts selection to the current project. Optional server slot restoration remains in the asynchronous preparation stage.
- Resume preparation is read-only. Cancelling it cannot replace the conversation later. Resume refuses an active agent/worker or buffered session writes.
- Full transcript is retained for TUI/history. A saved model projection plus its newer tail is used for inference. The decoded transcript is reused if no valid projection exists.
- Prior system messages are not duplicated, and transient tool visibility is reset to the selected session's discoveries.
- Checkpoint destination follows the selected session. Memory bookkeeping skips re-summarizing the loaded historical prefix; the complete history remains available in the session store.
- Done/error events remain visible immediately; a run is released only when its stream closes after persistence cleanup. Cancellation preserves the next draft and prevents queuing it into the stopped run.

## Drafts and images

- The latest unsent composer text and selected attachment paths for each project are stored in the application's portable data directory under tui-drafts.
- Writes are coalesced through a single 600 ms timer and flushed on ordinary exit and the existing Windows close callback. This is event-driven saving, not progress polling. A hard process kill can lose the newest fraction of a second.
- A restored draft reopens its saved conversation when it still exists. Starting a failed request does not overwrite text typed meanwhile.
- Windows Ctrl+V can read screenshots in PNG or uncompressed 24/32-bit DIB form. Images are encoded locally, saved under the portable clipboard directory and attached to the draft, without inference.
- Tab / Ctrl+K → Image from clipboard provides access when a terminal intercepts Ctrl+V.
- Identical image pastes reuse one file. Ordinary text paste and Explorer file paste remain available. Nothing is sent until Enter.
- Clipboard tests use generated memory fixtures; they do not overwrite the user's clipboard. Native interactive Ctrl+V/Windows dialog behavior was not manually exercised in this environment.

## Performance measurement

Command:
go test -run ^$ -bench BenchmarkResumeTranscriptReuse -benchmem -benchtime 400ms -count 3 ./internal/storage/session

Windows/amd64, Ryzen 7 5800X3D, synthetic persisted conversation of 400 messages:

| Resume transcript stage | Runs (ms/op) | Median ms/op | Allocated B/op | Allocations/op |
| --- | --- | ---: | ---: | ---: |
| Previous double read/decode | 1.435897 / 1.560892 / 1.572659 | 1.560892 | 2,464,438 | 7,393 |
| Reuse decoded transcript | 0.830377 / 0.904453 / 0.886260 | 0.886260 | 1,283,352 | 3,710 |

For this measured stage: approximately 43% less time and 48% fewer allocated bytes. This measures local resume processing; it is not a model-generation benchmark. No new prompt instructions or model calls were added.

## Validation

- Full go test -timeout 90s ./... passed.
- go vet ./... passed.
- After the final cancellation adjustment, affected TUI, agent and app tests passed and full vet passed again.
- Integration test: resume, continue, read original session through canonical store, attribute usage to the captured session.
- Tests: cancelled/superseded resume, pending-write protection, preserved drafts, coalesced draft persistence, stream-close ordering, image pixels/orientation/alpha/bounds and portable deduplicated image storage.
- Projection equivalence and damaged-projection fallback verified.

Windows reference documentation:
- [BITMAPINFOHEADER](https://learn.microsoft.com/en-us/windows/win32/api/wingdi/ns-wingdi-bitmapinfoheader)
- [Standard Clipboard Formats](https://learn.microsoft.com/en-us/windows/win32/dataxchg/standard-clipboard-formats)
