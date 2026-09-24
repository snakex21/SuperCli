# Preserve late matches in search context — 2026-09-24

## Defect and final behavior

search_code could find a match late on a long line, then erase it while adding neighboring lines. The context reader keeps only the first 2048 bytes of each line. Consequently a successful context=1 search for RetryPolicy returned a marked matching line containing only padding, without RetryPolicy or its value. This was reproduced from code inspection in a synthetic fixture; no recent production session is claimed as evidence for this specific defect.

The renderer now uses the matching text already captured by the same search when the first regexp match extends beyond that head boundary. It selects an excerpt around the match within the existing 2048-byte line budget and keeps surrounding lines. Full original matching lines are retained through the existing saved-output mechanism. Short lines and long lines whose first match is already visible retain byte-identical output. Explicit context=0 behavior, tool schemas and prompts are unchanged. The 500-line context cap remains; at most 500 long-line references are captured for this rendering path.

Automatic context now skips the neighborhood reread when a captured matching line already exceeds its 2048-byte total threshold. Such a neighborhood would previously have been built and discarded. The automatic model-visible result remains the original locations/preview.

Both ripgrep and the fallback scanner use the same rendering path. A failed ripgrep attempt clears captured long lines before fallback. Data belongs to one search invocation; subsequent searches see file edits. Matching text is a search-time observation, while neighboring lines are read afterward, as with the existing two-phase search; this does not create an atomic filesystem snapshot.

The excerpt cannot show an arbitrarily large match or every occurrence on a minified line. Full saved matches remain available subject to the existing output-store limits. No cache, extra inference, model-specific route or OpenCode Zen transport change was introduced.

## Controlled Qwen comparison

scripts/search-context-hit/main.go performs a fixed initial search with context=1 in a synthetic directory, then feeds its actual result to the real agent loop. Available tools are read-only search_code, read_lines and the registry's read_output. There is no shell or write tool. Timing begins after the fixed initial search and excludes its selection/execution; this is a continuation experiment rather than a complete project task.

The late fixture has RetryPolicy=6842 after 9000 bytes of padding on line 2 of settings.txt. The head control puts the same value before the padding. The short control uses an ordinary short matching line. Model: qwen3.8-27b-uncensored through LM Studio, reasoning requested at low.

| Case | Model calls before → final | Extra tools | Seconds before → final | Input tokens before → final | Initial model-result bytes |
| --- | --- | --- | --- | --- | --- |
| Late match | 4 → 1 | 3 → 0 | 21.386 → 2.558 | 6701 → 1292 | 2204 → 2306 |
| Visible head control | 1 → 1 | 0 → 0 | 2.604 → 3.057 | 1219 → 1219 | 2204 → 2204 |
| Short-line control | 1 → 1 | 0 → 0 | 2.875 → 2.819 | 944 → 944 | 97 → 97 |

All six baseline/final answers were correct. The late baseline first invented a saved-output handle from the file reference, then searched again without context, and finally queried the resulting valid handle. Thus its four-call path includes a model mistake, not three universally necessary recovery calls. The final candidate supplied the value directly. Saved-output metadata adds 102 bytes to the initial repaired result in this fixture; this is not a promise of smaller output on every request.

The first pilot changed all long matching lines, including visible head matches. It used one call / 2.528 s for the late case, but the head control unnecessarily reread the file and used two calls / 22.265 s (3147 input tokens). The short pilot used one call / 1.995 s (944 input tokens). All three pilot answers were correct. The final change was narrowed to actually hidden or cut matches, preserving the old head result exactly. Pilot artifacts are retained, not excluded from the record.

Run order: baseline cases, pilot cases, then final candidate cases. There is one baseline/final pair per case. Time, output length and cache state vary; the visible-head control is slightly slower in the final sample despite identical input. Input totals include cached tokens and are not monetary estimates. No live cloud run was performed for this change. Shared provider behavior is covered by native and thin/sentinel loop tests, not by a measured cloud latency claim.

## Local cost

Windows / Ryzen 7 5800X3D, fallback backend, four 100-iteration samples per variant in before/after/after/before order. The benchmark includes normal tool dispatch, backend discovery, file scanning and context rendering; it excludes output-store persistence and model inference. Values are sample medians.

| Fixture | Before | After | Allocated bytes before → after | Allocations before → after |
| --- | --- | --- | --- | --- |
| Late match | 1.392 ms | 1.341 ms | 190497 → 193242 | 329 → 350.5 |
| Visible head | 1.429 ms | 1.340 ms | 189960 → 191615 | 329 → 348 |
| Short line | 1.309 ms | 1.291 ms | 157579 → 158531 | 300 → 299.5 |
| Automatic long line | 1.328 ms | 1.285 ms | 189229 → 146603 | 327.5 → 297.5 |

Filesystem timings are noisy; no general local CPU speedup is claimed. Explicit long-line rendering adds a small map and regexp-processing cost, including in the visible-head control. Automatic long-line context avoids an otherwise discarded reread and reduces allocation in this fixture. Newly repaired results also retain full matching output; persistence cost was not benchmarked.

## Validation

- The original real-tool regression fails before the change because the matching value disappears.
- Tests cover late and boundary-crossing matches, UTF-8, CRLF, case-insensitive regexp and the fallback scanner's invalid-regexp literal mode.
- Short and visible-head controls stay byte-identical; automatic long-line output stays identical; a subsequent search sees a changed value.
- More than 64 matches are covered independently of the preview-record cap; long-line capture remains bounded, and failed ripgrep data cannot leak into fallback.
- The regression passes with the actual ripgrep binary as well as the fallback path.
- Agent-loop tests verify that the useful excerpt and neighbors reach both native and thin/sentinel protocols.
- Full go test -timeout=90s ./... and go vet ./... passed.

Raw artifacts under .tmp/search-context-hit/ include source snapshots/overlays, the failing baseline regression, targeted/full checks, pilot/final live runs, comparisons, benchmarks, source diff, release builds, smoke checks and installation verification.
