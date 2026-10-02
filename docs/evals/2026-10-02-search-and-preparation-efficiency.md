# Search and request preparation efficiency — 2026-10-02

This change follows read-only comparisons with the local DeepSeek, Pi, Codex and Claude Code source trees. It targets repeated data and work; it does not add a classifier, summary model call, background model, or system-prompt instruction.

## Complete grouped search results

Broad code searches repeated the same file path on every matching source line. The model now receives a clearly labelled grouped representation only when all captured records fit the existing 4 KiB preview budget and the reduction is at least 512 bytes and 25 percent. Every path, source line number, line body, record order and result-limit notice stays present. Sparse searches, errors, tiny gains and oversized complete groups retain their previous behavior.

A private Result.ModelText field carries complete small results without a stored-output handle, persistence I/O or a read_output hint. Both original and model representations must fit the inline limit; errors, explicit retained evidence and structured incomplete previews use the existing paths. Live tool result events and tool Result serialization continue to use the original Text. The complete grouped representation becomes tool-message history; GUI and TUI show that full grouped result after resuming the session. This avoids storing or reprocessing a second copy of repeated paths. Large results still retain their original file:line:content form for read_output.

Measured search fixtures:

| Fixture | Previous model result | Grouped model result |
| --- | ---: | ---: |
| 40 hits across two repeated paths | 4061 B | 2016 B |
| 20 hits in one repeated path | 2250 B | 955 B |
| 50 hits with a long repeated path, retained original | 4254 B | 1885 B |

These are byte counts, not billed token counts. Small-result formatting costs about 2.9 microseconds for 20 hits on this machine. No new file read or persistence operation is needed for a complete inline group.

Checks include real ripgrep/fallback parity, UTF-8, quoted paths, interleaved files, exact ordering, all rows, limit notices, unchanged errors and retrieval of large original output. An agent-loop test through both native and sentinel tool protocols also preserves the complete result and the live UI event.

## Matching files instead of repeated line hits

The optional search_code output_mode=files reports only unique file paths that contain the content query. It finishes scanning each file at the first match and counts the global limit in files. Existing default line results and no-query filename discovery are unchanged. The capability adds one small tool-schema property, not a system instruction or model call.

In the regression fixture, 80 README references previously consumed the 20-match limit before src/widget.go appeared. Matching-files mode reports README.md and src/widget.go in one call: 1113 B of line-mode model output versus 23 B of paths. For 32 files with one early hit and 8192 unrelated later lines, the Go fallback benchmark changes from 13.76 ms to 1.30 ms. This gain applies when only matching paths are requested; it is not an equivalent substitute when the task needs source lines.

Tests cover real ripgrep/fallback parity, includes and ignored directories, explicitly selected roots, sandbox and external paths, unique limits and duplicate records, empty/binary files, first-hit completion, errors, cancellation and timeout.

## Written patch evidence

The existing 1024 B / 16-line snapshot can now show the first and last differing regions of the actual final file without adding an output budget, reread or model call. A bounded 4 KiB suffix check avoids scanning a full large file for presentation. Unchanged long preceding lines cannot consume a region before its changed row appears. Missing middle content is explicit; this is never advertised as a complete diff, and a last change outside the bounded suffix keeps the original first-region view.

Block comparison also speeds up locating the first difference. In the 1 MiB fixture, a middle edit costs 258 → 19 us and an end edit 514 → 41 us. A single early edit stays about 1.53 us. Showing both distant regions costs 16 us because the final source line number must be counted; that new information may avoid a subsequent verification read, but a general reduction in agent turns was not measured.

## Preparation of a long history

The resolved-tool projection previously allocated a drop mask before it had found a completed tool batch and repeatedly parsed every old final response. The mask and recent-turn scan are now lazy, and visible replies older than the first identified final no longer need parsing. The output messages, indices, active tool pairs, errors, images and native continuation state remain identical.

Medians of three Go benchmark runs on Windows / Ryzen 7 5800X3D:

| History fixture | Before | After | Allocations before / after |
| --- | ---: | ---: | ---: |
| 80 short messages | 2.00 us | 0.60 us | 1 / 0 |
| 800 messages with long replies | 4.05 ms | 15.9 us | 402 / 1 |
| Long history with active tool tail | 4.05 ms | 16.1 us | 402 / 1 |
| Long history with completed tool evidence | 4.09 ms | 43.4 us | 404 / 4 |

For the long plain-history fixture, allocated bytes fall from 2.77 MB to 6.9 KB per projection. This measures local CPU and temporary allocation; it changes neither request tokens nor provider generation speed.

## Model comparison

LM Studio / qwen3.8-27b-uncensored completed six requests: three questions in both flat and grouped form, with alternating A/B order, temperature 0, seed 2718 and reasoning_effort=none. It received completed synthetic search results and could neither execute tools nor change source files.

| Question | Flat prompt tokens | Grouped prompt tokens |
| --- | ---: | ---: |
| Two files, same setting | 1257 | 880 |
| Find one value and source line | 1254 | 877 |
| Compare two values | 1261 | 884 |

Mean input: **1257.3 → 880.3 tokens**, **29.98% less**, as reported by LM Studio usage. All six replies contained correct full paths, exact source line numbers and assigned values; the numerical comparison was also correct. For the first two questions, both formats returned an assignment string in the value field instead of a bare JSON number. The initial prompt did not specify a numeric JSON type; the original strict-shape score (2/6) and the separate factual score (6/6) are retained in the local assessment rather than conflated.

This is a controlled small prompt fixture, not a claim about every session's bill or fewer full-task model turns. Six sequential requests also cannot separate warm-up and cache effects, so no provider-latency improvement is claimed. The special OpenCode Zen request path was not changed.

## Integration validation and artifacts

- All packages under internal/tools and internal/llm, plus internal/agent, internal/ui/tui, internal/webgui and cmd packages pass Go tests.
- Windows amd64, Linux amd64 and macOS arm64 CLI and GUI builds pass; Linux/macOS runtime UI was not exercised from Windows.
- Windows CLI and GUI --help smoke checks pass.
- The full diff whitespace check passes with CR-at-EOL accepted, preserving existing Windows line endings.
- Production version remains 1.0.1; this change does not publish a release.

Private reproducible artifacts are in .tmp/efficiency-harness-audit-2026-10-02: qwen-search-test-plan.json, qwen-search-live-results.json, qwen-search-live-assessment.json, integration-tests.json, patch-snapshot-final-benchmark.json, compact-build-results.json and install-status.json. Before/after source baselines and all raw benchmarks remain in the companion context-projection-audit-2026-10-02, search-grouped-preview-2026-10-02, search-files-mode-2026-10-02 and patch-two-region-2026-10-02 folders.
