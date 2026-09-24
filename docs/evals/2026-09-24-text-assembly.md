# Avoid copying already assembled text — 2026-09-24

## Finding and implementation

Both messageText and encodeOpenAIContent copied text Parts into a strings.Builder
even when the visible message consisted of one immutable string. The session
database contained 3,420 assistant messages with exactly one text part. That is
evidence that the representation is common, not that all those messages are
resent on every request.

The two helpers now measure the output while inspecting parts:
- If one existing string already is the final text, return it directly.
- If concatenation is necessary, grow one buffer to the final byte count.
- Preserve each helper's existing joining rules. messageText joins without a
  separator. Chat Completions inserts newlines after nonempty accumulated text,
  including before a trailing empty text part.
- Vision requests retain the existing multipart encoder; non-vision image
  placeholders and error behavior are unchanged.

Native reasoning payloads are not converted to text. There is no persistent
cache, prompt addition, model call, setting, history deletion or local-only path.
Only these two text helpers changed; the special Zen request transformations,
gate tools and headers remain untouched.

## Measurements

Windows / Ryzen 7 5800X3D, -cpu=1. Initial pair: three 200 ms samples per case.
Repeat pair: three 250 ms samples, reversed after/before order through a source
overlay. Inputs are constructed outside timing.

The text helper fixtures use a short Polish reply, one 8 KiB string, reasoning
plus that same string, and 64 fragments of 128 bytes. Selected initial medians:

| Helper case | Before | After | Allocated bytes before → after | Allocations before → after |
|---|---:|---:|---:|---:|
| Flat text, short part | 31.33 ns | 5.20 ns | 24 → 0 | 1 → 0 |
| Flat text, 8 KiB part | 776.9 ns | 4.94 ns | 8,192 → 0 | 1 → 0 |
| Flat text, reasoning + 8 KiB text | 749.1 ns | 5.68 ns | 8,192 → 0 | 1 → 0 |
| Flat text, 64 fragments | 3.917 us | 1.292 us | 34,176 → 8,192 | 11 → 1 |
| Chat text, 8 KiB part | 819.7 ns | 29.54 ns | 8,208 → 16 | 2 → 1 |
| Chat text, 64 fragments | 4.078 us | 1.296 us | 34,192 → 9,488 | 12 → 2 |

The Chat helper still allocates a small interface value; it is the full text copy
that disappears. Existing Content-only messages do not gain this saving. The
flat Content-only helper increased from 2.11 to 3.27 ns while remaining allocation
free; Chat Content-only stayed around 28 ns. The reverse pair corroborated the
part-copy reductions.

### Whole request encoding

The fixture has 80 alternating user/assistant messages, each containing 2,624
UTF-8 bytes, plus a small system message. Modes use plain Content, one text Part,
or eight text Parts. The benchmark includes the existing provider builder and
JSON encoding, but no network or inference.

| Case | Initial before → after | Repeat before → after | Initial allocated bytes before → after |
|---|---:|---:|---:|
| Chat, Content control | 0.394 → 0.435 ms | 0.390 → 0.408 ms | 386,293 → 386,293 |
| Chat, one text Part | 0.432 → 0.398 ms | 0.473 → 0.414 ms | 601,350 → 386,293 |
| Chat, eight Parts | 0.515 → 0.454 ms | 0.517 → 0.470 ms | 1,049,374 → 601,350 |
| Responses, Content control | 2.391 → 2.386 ms | 2.487 → 2.529 ms | 800,040 → 800,040 |
| Responses, one text Part | 2.453 → 2.447 ms | 2.529 → 2.545 ms | 907,574 → 800,040 |
| Responses, eight Parts | 2.566 → 2.616 ms | 2.727 → 2.664 ms | 1,153,238 → 929,229 |
| Anthropic, Content control | 7.836 → 8.178 ms | 8.002 → 8.139 ms | 3,846,112 → 3,846,153 |
| Anthropic, one text Part | 7.812 → 8.839 ms | 8.183 → 8.273 ms | 3,953,629 → 3,846,159 |
| Anthropic, eight Parts | 8.589 → 9.936 ms | 8.931 → 10.699 ms | 4,495,313 → 4,271,323 |

The Chat one-Part fixture allocates about 36% fewer bytes; eight Parts about
43% fewer. These are allocated bytes per operation, not resident/peak RAM.
Responses and Anthropic only flatten the assistant half of this fixture;
their user multipart representation is intentionally unchanged.

### Investigating the slower Anthropic control

Because both initial pairs showed slower Anthropic eight-Part encoding, four
additional fixed-count pairs were run, 100 operations each, alternating order:

| Pair | Order | Before | After |
|---|---|---:|---:|
| 1 | before, after | 8.553 ms | 8.105 ms |
| 2 | after, before | 9.604 ms | 8.259 ms |
| 3 | before, after | 8.749 ms | 9.415 ms |
| 4 | after, before | 9.510 ms | 8.553 ms |

Three pairs improved and one worsened; a consistent full-builder timing
regression was not reproduced. Allocation reduction remained stable (about
4.51 → 4.28 MB in these fixed-count runs). All results, including the slower
ones, are retained. We claim reduced copying and allocation, plus the measured
Chat fixture gains, not a universal whole-request speedup or lower provider
token cost. End-to-end response latency was not measured.

## Compatibility and validation

- Previous helper implementations serve as exact-value test oracles.
- All ordered triples from eight part types/contents plus 1,000 seeded mixed
  histories cover empty text placement, UTF-8, NUL/invalid bytes, reasoning,
  valid/nil images and unknown part types. Both vision modes retain the same
  values/errors and leave input messages unchanged.
- Nine whole-request fixtures across Chat, Responses and Anthropic have matching
  lengths and SHA-256 hashes before/after.
- Full go test ./... and go vet ./... pass; CLI and GUI builds pass.

No live-model calls were needed for this deterministic, content-preserving
change.

## Reproduce

go test ./internal/llm -run ^TestTextAssembly -bench ^BenchmarkTextAssembly
-benchmem -benchtime=250ms -count=3 -cpu=1

TEMP/TMP, build cache and evidence stay under the application folder.
Raw samples, source overlay, log-shape counts, wire hashes, validation and
installation records: .tmp/text-assembly.
