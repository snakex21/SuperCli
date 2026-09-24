# Session activity ordering and list cost — 2026-09-23

## Problem and behavior

The GUI ordered conversations by the timestamp of their first message. Sending
another message to an old conversation did not move it above newer conversations.
The date groups also used the first message date.

Lists now sort by the existing session activity timestamp; the GUI groups and
labels rows using that same timestamp. The original start timestamp remains
available separately. Opening a conversation without writing does not promote it.
Session metadata changes already update activity, so renames also affect ordering.

The server publishes one session_activity event after Loop.Run synchronously
persists the user message. The browser refreshes the list while the model is
working and keeps its existing completion refresh. No timer or session polling
was added. Requests superseded by a newer list request are aborted; sequence,
project and runtime guards also reject stale successful or failed responses.

## Query improvement

Project-filtered lists use the existing sessions(cwd, updated_at, created_at, id)
index. Message details are read only for selected rows. The old implementation
grouped complete message histories before sorting.

The query keeps EXISTS and an actual per-session message count to preserve
imported records whose denormalized message_count is stale. The unfiltered all
view retains the legacy message-only fallback and now orders it by latest
activity too; that fallback still groups messages. No migration is required.

Synthetic benchmark: 300 sessions in one project, 200 messages per session
(60,000 messages), request the newest 40 conversations. Windows amd64,
Ryzen 7 5800X3D; 10 measured iterations, three repetitions. Seeding excluded.

| Measurement | Before | After |
| --- | ---: | ---: |
| Median query time | 50.675 ms | 0.826 ms |
| Query time range | 49.812–51.839 ms | 0.812–0.897 ms |
| Allocated bytes per operation, approximately | 171,566 | 42,406 |
| Allocations per operation | 3,377 | 643 |

About 61× faster in this query benchmark. This does not measure model inference
or guarantee that complete conversations respond 61× faster. The change adds
no model request, prompt text or token cost.

Reproduce with Go's BenchmarkRecentProjectSessions in
internal/storage/session/recent_activity_test.go:
go test ./internal/storage/session -run '^$' -bench '^BenchmarkRecentProjectSessions$' -benchtime=10x -count=3 -benchmem

## Validation

- The ordering regression failed on the original implementation and passed after
  the fix: a reply in the oldest conversation must put it first even with limit=1.
- Storage tests cover project isolation, empty sessions, retained start times,
  activity timestamps, message-only legacy history, late session registration,
  and the actual project query's execution plan without a temporary sort.
- The real stream/API test verifies the updated list is available when the
  activity event arrives, even when the model never replies.
- scripts/test-session-activity.cjs exercises the actual GUI assets with a
  deterministic HTTP/SSE fixture in Chrome: promotion before completion,
  visible Dzisiaj group, active selection, opening without promotion, reload,
  and stale success/error responses after switching projects.
- go test ./... and go vet ./... pass; browser regression passes.

Raw outputs and the GUI screenshot are in .tmp/session-activity within the app.
