# Delegation activity restoration and translations

## Reproduced defect

The screenshot shows the names-only fallback in `renderTaskResult`: it has a
task notification and aggregated tool names, but no per-call activity DOM.
Live worker progress previously supplied expandable argument/result previews.
The canonical task result did not persist that trail, so rebuilding the card
from a transcript could retain only names and an unconfirmed status. This
reproduces the missing details after history restoration. It does not establish
that every possible live-stream interruption has the same cause.

## Correction

Each worker invocation captures its actual tool-call and tool-result events in
a bounded display journal. The immutable completed-invocation handoff carries
it through synchronous task results, continuations and asynchronous delivery.
It is saved by the existing canonical transcript write in `origin_json`; there
is no extra SQL request for each tool or new database migration.

The journal is display-only metadata. It is absent from provider requests,
model histories, resumed model-context reads and compaction input. It contains
no private reasoning. Existing report text, evidence and tool-result protocol
remain unchanged. No model, reasoning setting or generation budget is changed.

The transcript DTO exposes only host-owned task/worker metadata. The browser
checks its version and worker ID, renders individual calls lazily when the card
opens, and keeps success, failure and incomplete outcomes distinct. Tool text
is rendered as text, never HTML. A continuation retains its own journal without
changing a previous invocation's card.

Retention is limited to 64 latest calls and 128 KiB serialized per invocation.
Arguments and output/error previews are bounded with explicit truncation
markers. Omitted older calls are disclosed. This matches the preview purpose
of the existing live UI without retaining entire worker conversations.

Existing translated tool names now also cover counted fallback names and live
activity. Default worker titles use the existing delegation translation.
Changing language preserves expanded results. The partial-history explanation
is translated in all 27 GUI catalogs. Generated report contents and technical
custom agent/model IDs are preserved as supplied; they are not UI catalog keys.

Legacy transcripts containing only aggregated names keep their honest
names-only notice: this change does not invent missing arguments or outcomes.

## Verification

The new agent integration test runs real coordinator/worker loops with scripted
providers, executes fixture tools, saves to SQLite, closes/reopens the store,
and checks synchronous and asynchronous results plus a second worker run.
It verifies that saved previews contain actual tool data and no reasoning,
and that resumed provider/model histories contain no display journal.

A native headless browser test exercises the actual transcript DTO and GUI
scripts/styles with saved data. It checks lazy restoration, arguments, outputs,
errors, incomplete calls, cross-worker isolation, old records, language changes,
HTML escaping and preservation of the single human message. Existing browser
tests separately exercise live delivery and continuation routing. No external
model request or mutation of the user's existing sessions is needed.

Final verification passed:

- targeted worker handoff/report/history/catalog tests;
- `go test -race ./internal/agent -run ^TestWorkerUI -count=1`;
- `go test ./... -count=1 -timeout=10m` (329.51 s);
- `go run ./scripts/test-ui -timeout 7m`: 558 passed, no failures or skips;
- `go vet ./...` and `go build ./...`;
- CLI and GUI executable builds, CLI `--version` and hidden GUI `-h` smoke;
- scoped `git diff --check`.

During development, the first targeted test needed its canonical SQL decoding
corrected and legacy exact-result comparisons updated for the new UI-only
field. The first full run also exposed a browser fixture hardcoding the English
title `Worker 1`; its expectation now uses the translation key. The same
targeted commands and the full suite were rerun successfully after corrections.
The logs retain both failed and successful attempts.

Builds `supercli-worker-ui.exe` and `supercli-web-worker-ui.exe` were copied with
verified SHA-256 equality beside the existing executables, so their default
portable `supercli-data` directory stays the same. Existing standard executable
files were not replaced. Artifacts, logs and a browser fixture screenshot are
in `.tmp/worker-ui-2026-10-19/`; no commit or release was made.
