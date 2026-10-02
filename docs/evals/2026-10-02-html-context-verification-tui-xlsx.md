# HTML context, verification, TUI rendering and XLSX — 2026-10-02

This round reduces repeated argument decoding, repeated active TUI formatting and avoidable JSON preview scanning. It also makes the model representation of some completed command results cheaper and fixes named XLSX selection. Existing local/cloud provider routes, OpenCode Zen special handling and original live tool output are preserved. There are no added production model calls, system-prompt instructions, timers, background polling or application-data directories outside the portable app folder.

## Fewer prompt tokens for complete HTML command results

Go JSON normally encodes <, > and & as backslash-u escapes. In a tool-result string this adds tokens while preserving the same decoded values. For a successful complete ctx_execute result within the existing 8 KiB inline budget, the model-only representation can now keep these literal characters. An inexpensive scan requires at least 512 B of potential savings; the actual re-encoding gain is checked too, so literal backslash-u text does not accidentally trigger a representation change. Ordinary short/plain results take the existing no-allocation fast path.

Original Result.Text stays unchanged for live GUI/TUI evidence. Streams, canonical workdir, command, duration, exit code, capture flags and other metadata keep the same parsed values. The existing removal of a byte-identical long command echo still requires the paired argv to match. Errors, retained outputs and incomplete previews do not enter the new path. The change adds one re-encoding only in eligible results: the HTML fixture costs about 9 us and 4.8 KB of temporary allocation. The short plain fixture returns without an allocation in about 4.25 ns.

LM Studio / qwen3.8-27b-uncensored: two factual questions, each in both representations, alternating A/B order; temperature 0, seed 2718, reasoning_effort none and max_tokens 256. Inputs contained only a completed synthetic command result; the model had no executable tools or source access.

| Question | Previous prompt tokens | Literal-HTML prompt tokens |
| --- | ---: | ---: |
| Metadata, complete stderr and row count | 1415 | 728 |
| First/last IDs and limits, difference and exact cell text | 1424 | 737 |

Mean input is 1419.5 -> 732.5 tokens, 48.4% less in this HTML-heavy fixture. All four replies preserved all six checked facts for their respective question. Both representations parse to the identical complete result object. No full-task turn reduction, general billing percentage or provider-latency improvement is established by four requests; warm-up and cache effects are not separated.

## Less temporary RAM during verification

DefaultVerifier used to decode the entire write/patch argument map again for path and expected-content checks. One map is now reused for those checks and the existing empty-file compatibility rule. No new parser, key-matching policy or cache is added.

| Actual verifier fixture | Before -> after time | Before -> after allocated bytes |
| --- | --- | --- |
| 1 MiB content | 9.254 -> 4.481 ms | 2,098,896 -> 1,049,710 B |
| 20-change patch | 9.553 -> 4.765 ms | 2,200,217 -> 1,100,504 B |
| Small content | 20.34 -> 20.44 us | 1,984 -> 1,224 B |

The required single full decode remains. These are local verification CPU and temporary allocations, not total application RSS. Thirty-eight alias/case/escaped-key/duplicate-key/null/wrong-type/malformed/empty-file cases have the same verdict on baseline and new code.

## TUI active section reuse

Spinner-only refreshes previously re-ran Markdown, ANSI wrapping, styling and gutters for the entire unchanged active reply. One mutex-protected entry now stores only that section with its exact source, width, locale, symbols, reasoning-fold, style and color inputs. The spinner itself still paints. Completion, errors, tools, reset and run end detach the cache; completed history is not retained by it. Stateful custom style callbacks stay uncached.

Controlled single-CPU benchmark of actual Model.Update(spinner.Tick), streamFlushMsg and viewport.View, medians of three samples:

| Active text | Before -> after time | Before -> after allocated bytes per refresh |
| --- | --- | --- |
| 4 KiB | 674.3 -> 185.0 us | 412,742 -> 310,126 B |
| 32 KiB | 4.314 -> 0.345 ms | 1,364,776 -> 345,968 B |
| 128 KiB | 16.388 -> 0.850 ms | 4,286,280 -> 468,854 B |

New-source misses preserve accurate rendering and first-text immediacy. Controlled 4/32 KiB misses have the same allocation and approximately the same time. A 128 B miss adds about 2 us (3.5%); a 128 B spinner hit adds 2,650 B (0.9%) for exact style checks. The cache intentionally retains one formatted active section until completion. It is not an incremental Markdown parser and does not speed up provider generation.

## Shared backend JSON previews

PreviewJSONString now scans the preserved head and determines the tail boundary locally, rather than walking the omitted middle of every encoded log. JSON/rune/escape cut positions are byte-identical to the previous algorithm. An unusually long consecutive backslash run still requires scanning that run to determine escape parity.

Paired baseline/new benchmark in one Go process, three samples, 4 KiB preview budget:

| Encoded input fixture | Before -> after | Allocation |
| --- | --- | --- |
| Small unchanged input | 1.957 -> 2.102 ns | 0 B |
| About 4 KiB log | 3.595 -> 3.203 us | 4096 B, unchanged |
| 2 MiB ASCII log | 2.042 ms -> 3.210 us | 4096 B, unchanged |
| 2 MiB Unicode log | 2.957 ms -> 4.450 us | 4096 B, unchanged |

This CPU saving applies to command/process-result preview preparation shared by GUI and TUI. Encoding, output retention, network, WebView layout and provider processing are outside this isolated stage. Every-budget, long-backslash and randomized tests compare exact baseline output; existing command/process preview validity and diagnostics tests also pass.

## Correct named XLSX reads and one archive open

read_xlsx previously constructed a worksheet filename from its displayed name. Sales 2026 and Unicode names could therefore return a false empty success; a logical name sheet1 could return the wrong physical worksheet. Explicit names now resolve workbook metadata and relationship targets. Missing worksheets/relations return a real error with bounded available-name hints; a genuinely empty worksheet remains an empty success.

Default and numeric selection retain their existing physical sheetN meaning, as stated by the schema. Existing direct-entry-name fallback stays available. Local targets and declared/runtime entry-size caps are checked. The archive is opened once for metadata, shared strings and cells instead of reopening it.

A 2500-entry fixture changes from 1.850 to 1.234 ms and from 1.530 MB to 0.771 MB allocated per read. The small ZIP fixture changes from 0.735 to 0.682 ms and 26.0 to 19.7 KB. The tool definition shrinks 722 -> 590 B by removing implementation details while preserving parameters and clarifying worksheet selection. This definition-byte saving is not a measured prompt-token or task-turn saving.

## GUI follow-up measured but not implemented

GUI scheduleAssistantRender reparses the complete closed thought on every new answer delta. A read-only Node/V8 fixture with 256 prose packets costs 13.27 ms total for a 200k UTF-16-unit thought and 61.73 ms for 1M units. A tag-free theoretical suffix-only variant is about 1.2 ms total. A production change needs nested/split/orphan tags and replacement-recovery semantics; no new GUI parser is included in this round and these are not WebView screen-latency measurements.

## Validation and local artifacts

- PASS go test ./... and all 100 GUI tests (scripts/test-ui.cjs).
- PASS scoped TUI race, XLSX selection and full office suite, verifier semantic tests and HTML/model-result guards.
- All fourteen owned source/test hashes are identical before and after full tests.
- CRLF-aware git diff --check passes; unrelated edits remain preserved.
- Controlled model request/response bodies and factual scores: .tmp/efficiency-2026-10-02-round3/qwen-html-{plan,results,assessment}.json.
- JSON baseline, paired measurements and integration results: .tmp/efficiency-2026-10-02-round3/.
- Verifier baseline/measurements: .tmp/verifier-argument-reuse-2026-10-02/report.json.
- TUI exact-source overlays, measurements/tradeoffs and GUI read-only fixture: .tmp/tui-spinner-section-2026-10-02/REPORT.md.
- XLSX baseline, regressions, real tool texts and measurements: .tmp/xlsx-sheet-selection-2026-10-02/report.json.

Build and installation results follow after validation.

## Build and installation

All six stripped builds passed: Windows amd64, Linux amd64 and macOS arm64, each TUI and GUI. Linux/macOS were cross-compiled; runtime UI on those platforms was not exercised from Windows. Both Windows binaries passed --help smoke checks.

The previous Windows binaries were backed up before replacement. Existing hashes were checked before copying, and installed hashes match the build artifacts:

- supercli.exe: 26805248 B, SHA256 7b6e404f000df707016f7eb29cea87e7dead441f6094f62e74c81c3e2a9d41a0
- supercli-web.exe: 22428160 B, SHA256 63a1dae0b545d8815290206d93205bcc4f4816f07b8fe9a0faf6a5f9c0675691

All fourteen tested source/test hashes also matched after compilation. Version remains 1.0.1. No commit, push or release was made. Build/smoke/backup/installation manifests are in the round3 local artifact directory.
