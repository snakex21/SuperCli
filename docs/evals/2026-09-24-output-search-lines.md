# Complete matching lines in saved-output search — 2026-09-24

## Problem and change

The read_output search path used a fixed window of up to 160 bytes on each side of a literal match. A moderate line can fit the total window while its value still falls outside the fixed right half. The regression fixture has RetryPolicy near the start of a 261-byte line and effective_limit=6842 near its end. The prior search result shows the policy name and cuts off the value, despite enough space in its existing 331-byte excerpt budget.

The search now shifts that same window to include the complete matching line when its boundaries fit. Partial neighboring lines are trimmed where possible. It searches only within bounded nearby slices, preserves the complete literal match and UTF-8 boundaries, and leaves oversized/minified lines on the bounded byte-window fallback. Captured text, handles, raw byte reads, expiry/persistence behavior, search semantics and schema are unchanged. Excerpt-count limits and total byte budgets are unchanged; no instruction, helper inference, provider-specific path or Zen transport was added.

This is a reallocation of context, not a universal reduction of every snippet. In particular, selecting a complete line near EOF can include more useful bytes than the old shortened window, within the same cap. A line larger than the available budget still needs subsequent retrieval. Choosing the matching line can reduce neighboring context, whose full text remains retrievable.

The motivating mechanism was found by inspecting code after reviewing repeated saved-output reads in archived sessions. The specific long-line defect was reproduced in a synthetic fixture; it was not attributed to a particular archived user's command.

## Controlled live comparison

`scripts/output-search-lines/main.go` retains synthetic output through the actual registry OutputStore, executes a fixed initial search for RetryPolicy, and passes that actual result to the real agent loop. The task asks for effective_limit. Only saved-output retrieval is exposed: the model cannot run a shell or modify files. A short-line control already contains the answer in the old window; a minified control checks the old byte-window fallback.

| Model / case | Model calls before → after | Extra tool calls | Seconds before → after | Input tokens before → after | Initial result bytes |
| --- | --- | --- | --- | --- | --- |
| Qwen, long matching line | 3 → 1 | 2 → 0 | 12.025 → 2.492 | 3064 → 717 | 467 → 454 |
| Qwen, short-line control | 1 → 1 | 0 → 0 | 2.762 → 2.366 | 735 → 729 | 467 → 454 |
| Qwen, minified control | 1 → 1 | 0 → 0 | 2.240 → 2.499 | 823 → 819 | 467 → 467 |
| Muse, long matching line | not run → 1 | not run → 0 | not run → 1.848 | not run → 1023 | not run → 454 |

All seven completed answers were correct. Qwen baseline first reread 400 bytes from the excerpt's start, which still cut the value, and then searched again for effective_limit. The candidate supplied the value in the first snippet. Both Qwen controls stayed at one completion.

Muse candidate answered the main case, then received HTTP 429 on the short-line control and the harness stopped. Its remaining control and baseline were not run; there is no paired cloud speedup claim. The 429 attempt produced no answer and is excluded from correctness and latency comparisons. No immediate retry or provider switch was used to work around the limit.

Models: qwen3.8-27b-uncensored through LM Studio and muse-spark-1.3-contributor-free through Zen, reasoning requested at low. Qwen baseline and Muse candidate ran concurrently, followed by Qwen candidate. Cases within each model run were sequential. The baseline Go overlay restores only output_search.go. Each Qwen case has one before/after pair, starting after a fixed tool call, not an unconstrained project task. Model timing is variable; the minified control was slower after the change. Input totals include cached tokens and are not monetary cost estimates. Opaque handles differ between runs and can slightly change token counts even for otherwise identical controls.

## Local cost

Windows / Ryzen 7 5800X3D, 10,000 iterations per sample, four samples per variant in before/after/after/before order. The benchmark includes in-memory result lookup, searching and formatting; it excludes persistence I/O and inference.

| Fixture | Median before | Median after | Allocations/op |
| --- | --- | --- | --- |
| Long matching line | 1.194 µs | 1.195 µs | 10 → 10 |
| Short lines | 1.078 µs | 1.464 µs | 10 → 10 |
| Minified | 1.280 µs | 1.363 µs | 10 → 10 |

Allocated bytes remain about 1,417/op. The short-line case adds roughly 0.39 µs in this sample; no local CPU speedup is claimed. The intended saving is fewer model retrieval rounds when the fixed-centered window hid needed evidence.

## Validation

- The real tool regression fails before the change because effective_limit is cut, and passes afterward.
- Pagination tests cover LF/CRLF, multiple budgets, every literal-match position, increasing continuation offsets, exact correspondence between returned slices and stored byte offsets, and no budget growth.
- Controls cover UTF-8, multiline queries, absent final newline, query-sized budgets and byte-identical minified fallback.
- Existing overlapping-boundary, literal/case-sensitive search, cancellation, invalid arguments and expiry tests pass.
- Actual agent-loop tests verify the complete line reaches native and thin/sentinel protocols.
- Full `go test -timeout=90s ./...` and `go vet ./...` passed.

Artifacts under `.tmp/output-search-lines/` include the baseline source/overlay, failing regression, targeted/full checks, every live run including the provider failure, comparison, local benchmark, source diff, release builds/smokes and installation verification.
