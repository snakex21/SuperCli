# R13: orchestrator picker detached-owner audit

Status: approved narrow production port; integration tests pending.
Baseline: f58017476006cec9eeb52a67478e503f6a1f4beb; production 07-stats.js was confirmed byte-equal to 07-stats.baseline.js immediately before applying the measured overlay.

## Reachable owner

Each supercliOrchPicker factory installs a distinct anonymous document click listener that captures wrap/pop and the factory context. renderStats replaces its old container children; panel settings also constructs pickers. Old wrappers, opened model lists, search/row handlers and save closures remain reachable through the document listener. This grows with rebuild count. The generic helper is not limited to these two call sites.

The final prototype defines closeOrchPickerPopups outside the factory. At the original registration point it calls document.addEventListener with that same named function. Native EventTarget deduplicates it. Thus there is no startup registration, the first-use registration ordering stays unchanged, and the callback has no factory lexical context. The handler obtains getElementsByClassName('orch-pick') only locally during the click and closes each connected picker whose wrapper does not contain the target. No persistent collection, host-ID restriction, registry, cache, timer or extra picker-row callbacks are added. All fill/search/save code is untouched.

## Isolated native fixture

Used the installed Chrome headless runtime through existing bundled Playwright, without installation. Its new profile/temp/cache/crash directories are all under this ignored R13 directory. All routes abort; only synthetic setContent and inline production source are used. No user app, browser profile/window/tab, site, provider, clipboard or settings were accessed. The own browser context was closed in finally and the fixture process completed with exit 0. No live fixture handle remains.

The page has an arbitrary section host, 8,000 unrelated paragraphs, 20 model records (one hidden), two provider labels, and one connected picker during both measured phases. Every rebuild opens/materializes the dropdown; every third rebuild filters it before selecting a visible model. The 100-rebuild run therefore includes all row/search/save closures, not merely empty wrappers. WeakRefs cover all wrappers, including the first. Two awaited CDP garbage-collection commands precede the ownership read. Results are final native-results.json. The exploratory first run timed after a second picker was added; it is preserved separately and excluded from the following numbers.

## Results

| Shape | Document listeners | Retained wrappers | First wrapper retained | usedJSHeapSize | Median 1,000 public clicks |
| --- | ---: | ---: | --- | ---: | ---: |
| baseline, 1 rebuild | 1 | 1 | yes | 1,490,670 B | 2.0 ms |
| candidate, 1 rebuild | 1 | 1 | yes | 1,492,179 B | 2.1 ms |
| baseline, 100 rebuilds (A) | 100 | 100 | yes | 1,577,541 B | 48.7 ms |
| candidate, 100 rebuilds (B) | 1 | 1 | no | 1,378,858 B | 2.1 ms |
| candidate, 100 rebuilds (B) | 1 | 1 | no | 1,378,858 B | 2.1 ms |
| baseline, 100 rebuilds (A) | 100 | 100 | yes | 1,577,541 B | 49.3 ms |

The matched 100-rebuild native JS-heap difference is 198,683 B (about 194 KiB). Ownership counts are stronger evidence than total heap alone: the other 99 wrappers and the first detached wrapper cease to be reachable. usedJSHeapSize includes compilation and source state and is not native DOM/process RSS.

The generic class query has a measured cost after unrelated DOM mutation. Across 100 click-only timings, each immediately after an unrelated paragraph append, a fresh single-picker baseline totaled 0.4 ms versus candidate 4.6 ms (about 42 microseconds additional per click). At 100 rebuilds baseline totals were 5.0/4.9 ms and candidate 4.5/4.6 ms. Max individual measurements were 0.1–0.2 ms with roughly 0.1 ms clock quantization. Stable-DOM single-picker timings were close; stable-DOM callback accumulation after 100 rebuilds falls from about 49 microseconds per click to about 2.1 microseconds. This is a deliberate bounded tradeoff, not a zero-cost query claim.

## Behavior parity

All matching baseline/candidate control objects are deeply equal. Both arbitrary hosts work. Opening a second connected picker closes the first; clicking inside the second leaves it open; outside clicks close it. Model filtering, hidden records/provider labels, save callbacks, and selecting the already-active value without an extra save agree. Existing button/wrapper identities are retained through reopen and language-label refresh. Copying an unrelated code block preserves Unicode, indentation, newlines and HTML-looking source exactly. Observed activeElement identity is unchanged (BODY in both implementations); the helper's existing focus call while hidden is not fixed in this scope.

Full serialized HTML hashes agree for each shape: 1 rebuild 937bb0eb28d9ed6b1bf119854c779cb26af6ac23b0bc7c1c628c3304f6ff1887; 100 rebuilds efad13bc6a41bffea1604623e4d03b98d04a49888c7706f6660af9cccb06644a. No transcripts or user inputs are included in the generated fixtures.

## Limits and proposed port

This is a synthetic Chrome headless DOM/JavaScript test, not native WebView2 application RAM, FPS, terminal paint, TPS, token/prefill or end-to-end human latency. Actual user sessions may rebuild the picker fewer times; no claim of a fixed application-wide saving is made.

The source change is only the named handler and replacement registration in 07-stats.js. Durable UI regressions cover first-use registration ordering, deduplication, arbitrary hosts, multiple connected pickers, filtering/current-value save behavior, outside clicks, focus/button identity and collection of the first and subsequent detached materialized lists. The GC regression uses a separate Node process with --expose-gc, matching the existing UI lifecycle test pattern. Detached DOM is deliberately not retained just to hide its invisible popup. The full UI suite is delegated to root after file freeze.
