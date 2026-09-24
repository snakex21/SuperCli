# Avoid loading plain transcript text for statistics (2026-09-23)

## Problem and change

The GUI statistics service loaded every encoded message and decoded the entire
transcript before counting roles and tool calls. This happened even for modern
sessions whose context/token breakdown already exists in session_usage. Large
plain tool results were allocated in Go and retained through the statistics
call despite contributing only one to the tool-message count.

For sessions with recorded usage, ReadMessageCounts now streams just the fields
needed for structural validation and counting. SQL returns the non-emptiness of
plain content using octet_length(content), rather than transferring its text.
Embedded NUL still counts as nonempty. PartsJSON and ToolCallsJSON are decoded
through the existing ToMessage/Validate path, preserving rejection of malformed
historical messages. SQL NULL content still produces a scan error, as before.

The GUI reads usage first and uses this lighter counter when usage exists.
Legacy sessions without usage retain the original full-text path because their
context estimate actually needs the text. Session metadata, cost calculation,
token totals, provider attribution and context breakdown are unchanged.

No schema migration, write-time work, persistent counter, invalidation cache,
model prompt or inference was added. Counts are freshly read, so append and
rewind are immediately reflected. Workspace access checks still run before
reading session data. Provider transports, including OpenCode Zen, are unchanged.

## Paired measurement

Windows amd64, Go 1.26.2, Ryzen 7 5800X3D. The fixture reuses the session-capsule
benchmark conversation: six messages per turn, including two roughly 16 KiB
plain tool results and a text-parts assistant reply. Each turn also has one
persisted usage record.

The measured operation is Engine.stats, including message counts, recorded
usage reads, token/context/cost calculations, telemetry and daily totals. Each
result is compared with the initial complete stats structure. Fixture creation,
engine startup and the initial warm-up are outside timing. HTTP serialization
and browser rendering are not measured.

Separate before/after executables use the same benchmark. The baseline overlay
replaces only feat_stats.go with its original version; the new storage function
is present but unused there. Order: before/after, after/before, before/after.
Each run uses 100 iterations. Medians of three runs:

| Session | Before | After | Allocated bytes before | Allocated bytes after |
|---|---:|---:|---:|---:|
| 10 turns / 60 messages | 2.653534 ms | 2.166483 ms | 477,423 | 77,394 |
| 200 turns / 1200 messages | 16.160018 ms | 10.066221 ms | 9,081,939 | 1,050,556 |

The longer fixture spends about 38% less time and allocates about 88% fewer
bytes. Allocation count falls from 26,969 to 21,313. The short fixture saves
about 18% of elapsed time and 84% of allocated bytes.

An initial SQL CASE/content-comparison version reduced Go allocations without
improving latency. Profiling showed remaining database work; the final version
uses stored byte length for plain-content emptiness. Only the paired final
measurements above support the reported speedup.

This reduces local GUI statistics overhead, not inference time or provider
charges. The query still scans the selected session, and complex parts/call
payloads still require decoding. Recorded usage and other statistics work remain
proportional to their own data. Legacy sessions do not use the new path.

## Correctness and validation

A corpus of 1920 combinations compares counts with full ReadMessages/ToMessage
decoding: all roles plus an invalid role, empty/whitespace/NUL/Unicode content,
valid/invalid text/image/reasoning parts, malformed JSON, empty call lists and
calls with missing or wrongly typed fields.

Additional checks cover SQL NULL behavior, empty/missing sessions, cancellation,
session isolation, fresh counts after appending and rewinding, and unchanged
legacy context estimation. GUI integration tests verify recorded token/cache/
reasoning usage and compare every session-summary field with the old full-text
calculation. Existing workspace and provider/cost tests pass.

Full go test ./... and go vet ./... passed.

    go test ./internal/storage/session ./internal/webgui
    go test ./internal/webgui -run '^$' -bench '^BenchmarkStatsLongSession$' -benchtime=100x -count=3

App-local evidence: .tmp/stats-read-cost/paired.json, before-overlay.json,
feat_stats.go.before, benchmark-builds.json, focused.json, optimized.json,
checks.json and the local CPU profile.
