# Prefer native tool calls without removing the text fallback

## Evidence

The unpruned 64k continuation trace still repeated tool discovery and checks. The thin-tool preamble began by requiring an exact text format "not JSON", even while native schemas were sent for the core tools and a later sentence explicitly required native JSON for arrays/objects.

A fixed-history comparison kept the same synthetic code, prior task/results, tools and 65,536-token window. A full native-tools arm was initially useful for diagnosis: Muse completed in 6 requests versus 15 with the old thin preamble; Qwen used 4 in either case. This was not used to change the global tool mode.

Instead, an evaluation-only replacement tested a shorter preamble that prefers available native calls, while retaining the sentinel format, no-argument calls, batching, array/object rule and direct read-only dispatcher syntax. Two Muse comparisons were run with reversed before/after ordering on the repeat.

## Measured follow-up trials

All rows produced correct code, unchanged passing supplied tests and a final report. No result pruning occurred.

| Model / variant | Model requests | Input tokens | Worker time |
|---|---:|---:|---:|
| Muse, old preamble 1 | 15 | 170,218 | 62.108 s |
| Muse, shorter native-first 1 | 6 | 55,916 | 16.304 s |
| Muse, shorter native-first 2 | 12 | 117,371 | 38.526 s |
| Muse, old preamble 2 | 15 | 165,678 | 54.323 s |
| Qwen, old preamble | 4 | 27,954 | 48.735 s |
| Qwen, shorter native-first | 4 | 28,025 | 43.827 s |

Muse's two old-preamble runs made 5 and 2 tool_search calls (including one empty discovery call in the first run); the replacements made 0 and 1. The second old run also repeatedly emitted an empty invoke_tool after the correct edit/check and eventually reported the loop blocker. The replacement still had redundant checks/reads, and its second run encountered a missing-line read_context error. These are remaining issues, not claimed fixed by the wording.

Qwen's replacement batched an additional source read and stayed at 4 requests. Its input total was slightly higher despite the shorter fixed preamble. No local token or latency speedup is claimed from that single pair.

Provider sampling and wall-clock load remain uncontrolled. The observed Muse gains apply to this synthetic continuation fixture, not to all sessions or models.

## Production change

ThinToolProtocol now says to use native calls when available, followed by the text fallback. Its UTF-8 length falls from 590 to 441 bytes (149 fewer, about 25%). No new instruction block, model call, dynamic repetition monitor, schema, permission, or provider-specific branch was added. Thin/native settings, discovery and dispatch are unchanged; the special Zen serializer and headers are unchanged.

The existing prompt-format test was updated to require both native precedence and the sentinel fallback. The GUI process-continuation fixture now identifies the active preamble via the shared constant instead of a hardcoded old sentence. Its first failure during full validation was that obsolete fixture assertion, not a process-resume regression.

The optional live evaluator retains frozen legacy/native-first variants so the A/B can be reproduced after rollout, with protocol/window/seed explicitly recorded.

## Fresh validation and delivery

Fresh runs without a seeded initial stage use the production instruction directly:

- Muse: 8 initial requests + 8 follow-up requests; no failed tools, correct code, unchanged passing tests and final reports.
- Qwen: 5 + 4 requests; same correctness checks, no failed tools.

These fresh runs overlapped local test/build work and are correctness checks, not comparable latency benchmarks.

Full go test -timeout=90s ./... and go vet ./... pass after updating the GUI fixture. CLI and GUI builds pass; both portable executables were installed after a CLI --help smoke check.

Artifacts: .tmp/worker-continuation-protocol-2026-09-27/ contains all model results. .tmp/native-first-tool-hint-2026-09-27/ contains before files, full validation, binaries and installation records.

Next concrete finding: the recorded read_context call with file/radius but no line fails with line=0, then is followed by read_lines. Investigate the schema/default contract before changing read behavior.

Measurement scope clarification: these live thin fixtures used the supported tail catalog placement. GUI/TUI defaults already hoist a stable catalog; see [production-profile follow-up](2026-09-27-worker-production-profile.md) for the corrected evaluator and live checks. Within-variant comparisons above remain scoped to their recorded placement.
