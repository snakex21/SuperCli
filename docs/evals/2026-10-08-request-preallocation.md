# OpenAI-compatible request array reservations — 2026-10-08

The OpenAI request builder grew message, tool-definition and assistant
tool-call arrays through repeated `append`, despite knowing their counts.
It now reserves those three capacities before filling the arrays. The message
count comes after existing system-message demotion and tool-history repair.
Zero counts retain nil slices, preserving omitted/null JSON fields.

This is a small local request-construction and allocation improvement. It
does not reduce prompt tokens, provider prompt processing or provider wall
time. Existing history projection/compaction, copy-on-write reasoning
filtering and bounded schema normalization cache remain in place. There is
no additional history copy, serialized-prefix cache, option or instruction.
Zen's route/gate, AnyRouter routing and native/signed-state handling are
unchanged.

## Isolated measurement

A test-only Go overlay compared the real builder with a generated copy
differing only in those three reservations. Both used the production
normalization, repair, reasoning and content helpers and final JSON encoding.
Full-byte/error parity and caller-immutability tests passed, including nil
arrays, malformed histories/schema/arguments, native and opaque input,
existing Zen gate/scope filtering and image fallback. The overlay and builder
copy remain temporary experiment artifacts; no copy was added to durable tests.

Go 1.26.2, Windows/amd64, Ryzen 7 5800X3D, GOMAXPROCS=2; medians of three
300 ms repetitions on synthetic fixtures:

| Fixture | Previous time | Reserved time | Previous B/op | Reserved B/op |
| --- | ---: | ---: | ---: | ---: |
| 1,703 messages / 12 definitions | 4.130 ms | 3.957 ms | 6,382,539 | 4,713,618 |
| 422 messages / 12 definitions / batches of 8 calls | 696 µs | 636 µs | 778,163 | 633,535 |

The parallel fixture dropped from 681 to 541 allocations per operation.
Small-request timing was neutral/variable while allocations fell. These
synthetic requests are not saved live wire snapshots, and the archive-sized
fixture is larger than many requests after existing history projection.
JSON encoder pooling contributes to allocation variation; these results do
not establish a universal CPU improvement or an end-to-end session speedup.
The investigated session previously recorded approximately 5.68 seconds of
request encoding across 858 model calls in approximately 226 minutes.

Artifacts: `.tmp/optimization-oct8-2026/request-prealloc-experiment/` and
`go-request-prealloc-0.log` / `go-request-prealloc-1.log` in the parent directory.
Production edits were formatted and frozen; full `internal/llm` validation
is pending centrally.
