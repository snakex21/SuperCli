# Preserve useful hits in large search results

Date: 2026-09-24.

## Problem and change

In the inspected session database, 72 of 778 search_code tool messages contained
a retained-output marker. The old generic preview keeps the beginning and end
of a result, so two long matching lines can hide a short useful hit between
them. Some historical searches included generated directories excluded by
earlier fixes; the count is not an estimate of currently wasted model calls.

The local Pi harness also bounds individual grep lines
(pi-main/packages/coding-agent/src/core/tools/grep.ts). This change adapts that
idea to SuperCLI's existing saved-output mechanism:

- Only successful location results above the existing 8192-byte inline threshold
  are candidates. Small results and tool schemas remain unchanged.
- Reserve complete path/line references, then allocate the existing 4096-byte
  preview budget to short hits first. Each longer line gets at most 512 bytes,
  centered near the first regex match, with ellipses for omitted text.
- Keep the original hit order and match-limit warning. Paths are never shortened.
  If metadata does not leave at least 64 bytes per hit, use the existing generic
  preview. More than 64 hits also use that fallback; metadata capture is bounded.
- Text stays intact for the UI and retained-output store. read_output can still
  retrieve/search the original result without re-executing the search.
- Explicit expanded context retains its existing behavior. Both native Go and
  ripgrep backends capture unambiguous records, including context=0.
- No new prompt, tool parameter, model call, cache or provider-specific branch.
  OpenCode Zen transport and special handling were not modified.

This is a source-evidence improvement, not a guarantee that every model will
stop verifying information it can already see. Other matches or needed context
on a shortened line can still require reading the saved result.

## Regression and live comparison

The regression fixture has three matching lines: two about 12 KB long, with
their searched values in the middle, and one short line in the middle file.
The baseline loses the short hit and the matched values in its generic preview.
The candidate preserves all three file/value associations, including UTF-8 text.

The live runner (scripts/search-hit-preview) fixes the initial real search_code
execution and measures the real agent continuation. Models receive only
synthetic fixtures and read tools; their initial choice of search query is
not being measured. Settings and prompts are identical before and after.
There is a six-step experimental ceiling and a small-result control.

Large captured result: 24091 bytes. Model-visible result including retrieval
metadata: baseline 4387 bytes, final candidate 1327 bytes (about 70% smaller).
Small-result control: 87 bytes in both versions.

Final candidate compared with the two collected baseline runs:

| Model / version | Continuation calls | Tool attempts | Input tokens | Seconds | Correct |
|---|---:|---:|---:|---:|---|
| Qwen baseline 1 | 2 | 1 | 4777 | 14.118 | yes |
| Qwen baseline 2 | 3 | 2 | 7851 | 24.187 | yes |
| Qwen final | 1 | 0 | 1613 | 19.047 | yes |
| Muse baseline 1 | 6 | 6 | 30450 | 29.514 | no final answer: step ceiling |
| Muse baseline 2 | 3 | 2 | 10455 | 11.268 | yes |
| Muse final | 4 | 3 | 15931 | 18.269 | yes |

Qwen used the visible evidence without another read in the final run. Muse
still read the saved output in three chunks despite all requested values being
visible; this change did not establish a turn or latency benefit for Muse.
The first Muse baseline also attempted unavailable ctx_execute; it was rejected
by the read-only registry and no shell command was executed.

The first pilot without the 512-byte per-line ceiling produced a 4277-byte
preview. Qwen used 1 and 2 calls across its two pilot runs, Muse 4 in both.
The ceiling reduced irrelevant padding further. All completed candidate answers
were correct. The final small-result controls used one call each (Qwen input
1089, Muse 1399), matching baseline 1 for Qwen and baseline 2 for Muse.
A pilot Muse small control used two calls even though that output is unchanged.

These are small controlled continuation experiments, not end-to-end project
benchmarks or a statistical speed estimate. Wall times varied substantially:
even Qwen's identical small control ranged from about 4 to 29 seconds across
baseline runs. No consistent overall latency win is claimed. Token totals
include cached input and are not billing estimates.

## Local overhead and checks

A ten-file Go scanner microbenchmark ran 3 x 30 iterations per variant.
For 12 KB matching lines, median allocated bytes rose from 665565 to 826674
(about 161 KB per ten-file search). Recording excerpts has a local cost; this
is not a CPU-allocation optimization. Small-result allocations stayed around
140 KB, with roughly 14 extra allocations. Warmup/order and filesystem noise
made timing unsuitable for a CPU-speed conclusion; raw measurements are saved.

Regression coverage includes:

- The short middle hit and late matches remain visible, with the original
  result still retrievable through read_output.
- Native and sentinel agent routes deliver the same useful evidence.
- Real ripgrep and Go fallback produce identical previews and preserve limits.
- UTF-8 boundaries, the 8192-byte threshold, complete path references, bounded
  metadata capture, and discarding partial rg records before fallback.
- Existing search/context behavior and the full repository suite.

Validation passed: go test -timeout=90s ./... (with real-rg tests enabled),
go vet ./..., CLI/GUI release builds and startup smoke checks. Installation
hashes and backups are recorded alongside results.

Local artifacts: .tmp/search-hit-preview/ contains the baseline source overlay,
failing baseline regression, successful tests, raw pilot/baseline/final model
answers and tool attempts, microbenchmarks, build checks and installation record.
