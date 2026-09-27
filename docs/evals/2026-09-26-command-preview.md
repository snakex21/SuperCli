# Structured large command previews

Date: 2026-09-26. Shared ctx_execute path; local/cloud and CLI/TUI/GUI.

## Evidence

Five large ctx_execute previews in the fixed session snapshot use the generic 3 KiB head / 1 KiB tail cut on serialized JSON. Repeated command and metadata occupy 279, 416, 369, 499 and 468 bytes of the tail. The saved examples still contain visible exit-code text; this finding does not prove a resulting rerun.

A real subprocess fixture emits separately marked stdout/stderr with a long command argument. The old generic cut retains only one of four stream endpoints and produces incomplete JSON. A retained-capture variant retains two of the three endpoints present in the bounded UI output.

## Change

Only successful command results whose existing JSON exceeds the unchanged 8 KiB inline threshold receive a structured preview. The unchanged 4 KiB body budget covers valid JSON, outcome, duration, explicit preview/truncation flags and both streams. The smaller stream releases unused space to the larger one. Cuts preserve complete JSON escapes and UTF-8 units, with explicit omission markers. Command/workdir stay in the retained original and UI result; the model already has its paired call.

Original Text, RetainedText and capture limits remain unchanged. The existing output store retains the original once and supplies its usual read_output handle. Small results and failure summaries use their existing path. No provider/Zen changes, extra model calls or longer tool instructions. The previous pruning fix can now decode these complete JSON previews and preserve success status after pruning as well.

## Tests and measurements

- Real subprocess, red/green: large result goes from 4,387 bytes / 1 endpoint to 4,275 bytes / 4 endpoints.
- Retained capture: 4,324 bytes / 2 endpoints to 4,275 bytes / 3 available endpoints. The earlier captured detail remains retrievable without rerunning the command. The preview does not bypass the requested stream cap.
- Small result stays byte-identical; errors keep the generated failure summary.
- Cross-product tests cover plain text, Unicode, invalid UTF-8, quotes, backslashes, control characters, HTML escaping and U+2028/U+2029. Every possible cut in a mixed escaped sample remains valid bounded JSON.
- Native/thin provider-view tests preserve known success and timeout outcomes after pruning with one original invocation.

Local benchmark, Windows / Ryzen 5800X3D, median of three 300 ms samples:

| Fixture | Generic | Structured | Allocated bytes | Allocations |
| --- | ---: | ---: | ---: | ---: |
| Small | 1.626 us | 1.618 us | 3,187 -> 3,187 | 3 -> 3 |
| 16 KiB stdout | 36.267 us | 93.784 us | 82,818 -> 92,218 | 7 -> 16 |

The large-output preparation adds about 0.058 ms locally in this fixture in exchange for preserving both streams and valid status-bearing JSON. This is an evidence-preservation improvement, not a measured end-to-end speedup. No saved-turn or live-provider latency claim. The small-case variation is measurement noise.

Artifacts: .tmp/command-preview-2026-09-26/{before,after,retained-tests,encoding-protocol-tests,benchmark,snapshot-evidence}.json.

Full go test ./... and go vet ./... passed. CLI/GUI builds and CLI --help smoke passed. Both installed executables match their build hashes; previous binaries are backed up under .tmp/command-preview-2026-09-26/before. Restart running instances to use the change.
