# Read only the source used by session memory (2026-09-23)

## Problem and change

After a GUI turn, saveWebSessionCapsule read the entire transcript, decoded all
user/assistant messages, and then retained the first useful user message and
last eight useful dialogue messages. Long tool outputs were loaded into Go even
though the capsule immediately ignored them. This work repeated after each turn.

ReadDialogueExcerpt now walks dialogue rows from the end until it has eight
usable messages. When needed, it separately finds the earliest usable user
message. Both reads share one SQLite transaction/snapshot. SQL filters out
system/tool roles before transferring their payloads. The result retains at
most nine source rows in chronological order, with no duplicate first message.

The existing capsule text conversion decides which rows are useful. Empty,
malformed, image-only, reasoning-only and tool-call-only entries cannot displace
an older useful reply merely by occupying the last eight database rows. Text
stored in either Content or PartsJSON is still accepted. The capsule formatter,
4200-byte limit, memory identity, retention, timestamps and writes are unchanged.

This optimizes GUI post-turn project-memory maintenance for every provider.
There is no extra prompt text, inference, background job, persistent cache,
database migration or local/cloud branch. The conversation archive and provider
request paths (including OpenCode Zen) are untouched. The TUI does not call this
GUI capsule writer.

## Paired measurement

Windows amd64, Go 1.26.2, Ryzen 7 5800X3D. Synthetic sessions contain six messages
per turn: a user request, two assistant tool calls, two roughly 16 KiB tool
outputs, and an assistant text-parts reply. The longer session has 1200 rows.

The benchmark measures the complete saveWebSessionCapsule operation, including
the read, text selection, existing memory lookup, retention and durable memory
update. Engine creation, fixture insertion, initial warm-up and final equality
checks are outside the timed loop. The same completed session is updated
repeatedly; this isolates local maintenance cost rather than simulating model
generation or growing the archive while timing.

Separate test executables used identical fixtures. The baseline Go overlay
replaced feat_memory_runtime.go with its pre-change copy and excluded the new
GUI regression test file that references the extracted helper. The new storage
reader is present but unused by the baseline. The workspace stayed on new code.

Order: before/after, after/before, before/after; 50 iterations per benchmark per
run. Values below are medians of the three runs for each version.

| Session | Before | After | Allocated bytes before | Allocated bytes after |
|---|---:|---:|---:|---:|
| 10 turns / 60 rows | 2.320686 ms | 1.872154 ms | 475,103 | 62,896 |
| 200 turns / 1200 rows | 18.276678 ms | 1.920586 ms | 8,828,671 | 63,338 |

For the longer fixture, time falls about 89.5% (9.5x faster) and allocated bytes
about 99.3%. Allocation count falls from 22,822 to 989. The short fixture saves
about 19% of elapsed time. The first unpaired baseline had noisier short-session
disk timings, so paired medians are used for claims.

These are post-turn local maintenance measurements, not first-token or model
generation speedups. A sparse/damaged transcript can require scanning more rows
to find eight useful messages; scanning remains cancellable. SQL may still
inspect excluded rows, and unusually large dialogue/image payloads still cost
work when decoded. No universal constant-time or fixed-byte bound is claimed.

## Correctness checks

Tests compare capsule text byte-for-byte against the existing formatter over
the full transcript. Cases include short/no-user sessions, Unicode and emoji,
text parts, sparse useful replies, tool-only exchanges, reasoning, malformed
stored JSON, image-only messages and the final text cap.

Storage tests cover ordering, limits, session isolation, early stopping, missing
sessions, cancellation before and during scanning, and a concurrent write
between reading the tail and the first user. The latter confirms both edges
come from the original snapshot and the next call sees the new data.

Integration checks append a new result and rewind the session, then compare the
saved memory with the full-history result. The archive remains byte-for-byte
unchanged by capsule maintenance. Existing restart/recall tests also pass.
Full go test ./... and go vet ./... passed.

    go test ./internal/storage/session ./internal/webgui
    go test ./internal/webgui -run '^$' -bench '^BenchmarkWebSessionCapsule$' -benchtime=50x -count=3

Evidence retained inside the app: .tmp/session-capsule-cost/paired.json,
before-overlay.json, feat_memory_runtime.go.before, benchmark-builds.json,
focused.json and checks.json.
