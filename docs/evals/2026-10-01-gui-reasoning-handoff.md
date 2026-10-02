# GUI reasoning handoff and frame ordering — 2026-10-01

The reported text jitter could be reproduced in the real Windows WebView2 window without a provider request. The GUI displayed the first answer packet immediately while continuing to reveal previously received reasoning above it. Separately, the scroll callback could read the previous frame's geometry before the paced text paint, leaving the viewport one wrapped line behind.

## Changes

- Finish queued text in preceding sections when a new section starts. Reasoning and the first answer are painted together at this boundary; received source and channel classification stay authoritative.
- Keep the open paragraph element and its text node for ordinary prose. For formatted prose, reuse the original Markdown renderer and append only unchanged-prefix HTML suffixes; revise the current paragraph when newly completed inline markup changes its interpretation. Lists, tables and fenced code retain their existing dedicated paths.
- Perform the pending tail scroll after paced text writes in the same animation frame. Tool/SSE bursts still share one scheduled scroll. Manual scroll-away and explicit return-to-tail behavior remain covered.
- Keep CR/NUL inputs on the HTML parsing path so browser text normalization matches the full renderer.

## Controlled native comparison

The same 1200 × 820 WebView2 fixture, CSS, font and workload were used before and after. These are rendering measurements, not model-generation speed or a guarantee against every OS/provider pause. Timer resolution limits interpretation of sub-0.1 ms samples.

| Measurement | Before | After |
| --- | ---: | ---: |
| Reasoning mutations after first answer | 100 | 0 |
| Answer position drift after first answer | 221.6 px | 0 px |
| Tail-follow frames more than 1 px behind | 60 / 160 | 0 / 160 |
| Maximum tail shortfall | 24.33 px | 0.33 px |
| Plain paragraph replacements, 240 packets | 240 | 0 |
| Formatted paragraph replacements, 240 packets | 240 | 0 |
| Plain rendering p95 | 0.2 ms | 0.1 ms |
| Formatted rendering p95 | 0.9 ms | 0.3 ms |
| Plain DOM mutations | 480 | 240 |
| Formatted DOM mutations | 480 | 241 |

Median native display cadence remained approximately 6.3 ms. Scroll geometry read count stayed at 185 in both runs; the correction concerns ordering rather than skipping required geometry reads. The first answer was outside the reasoning block and byte-for-byte unchanged in both cases. The visual overlap came from the still-growing thought above it.

An intermediate plain-text appendData variant increased rendering cost to about 1.1 ms p95 and was rejected. Updating the retained text node's data avoided that regression. The fixture's script embedding and a CR-normalization difference were repaired before accepting the final native parity results.

## Validation

- 73 UI tests passed, including phase completion, Unicode, recovery snapshots, code/list/table rendering, manual scroll-away and detached pending paints.
- 933 prefix/identity checks passed in the actual WebView2 DOM, with full-render parity and lazy history reasoning preserved.
- `go test ./internal/webgui` passed.
- Windows GUI build succeeded.

The diagnostic executable and all its profiles/results are portable inside `.tmp/gui-reasoning-handoff-2026-10-01`. No model prompt, provider request or Zen routing change is needed for these fixes.
