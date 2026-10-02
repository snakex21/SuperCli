# WebView2 folded-result memory and panel lifetime

The default desktop renderer remains WebView2. This change reuses the existing Go engine, embedded frontend and provider path; it introduces no runtime dependency or model prompt text. Reviewed and fast-forwarded the other agent’s `cfd43ba` and `e83ca33` commits before validation.

## Changes

- Live tool results use the same deferred viewer mechanism as persisted history. Completed folded read/diff/command rows retain their original payload and compact status, but materialize the detailed DOM only on expansion. Already-open rows render immediately, including error diagnostics. Worker activity behavior is unchanged.
- Dynamic translation refreshers are owned by their DOM node instead of a global array. Removed About/update/doctor panels no longer remain referenced until the next language switch. Connected panels still update in place.
- The provider panel returns its rendering promise to the existing asynchronous tab completion guard.
- The exhaustive search-ancestor test normalizes its Windows stopping point, preventing an infinite fixture loop when TEMP contains forward slashes. No production search behavior changed for this fixture correction.

## Native WebView2 fixture

One before/after pair used the same diagnostic executable and real embedded frontend, with fresh app-adjacent data and WebView2 profiles. The baseline substituted only the saved pre-change `01-i18n.js` and `04-transcript.js`. No provider inference was used. Each phase was sampled after a fixed two-second idle; no progress polling. The fixture created ten completed, folded `read_lines` results of 5000 lines each, then expanded one result and verified all 5000 rows including the last line.

| Measurement | Before | After |
| --- | ---: | ---: |
| Folded transcript elements | 150080 | 70 |
| Detailed file viewers while folded | 10 | 0 |
| Detailed file viewers after opening one result | 10 | 1 |
| JavaScript time creating ten result cards | 133.3 ms | 1.2 ms |
| Private resident process-tree memory before results | 164.1 MiB | 138.7 MiB |
| Private resident process-tree memory with folded results | 237.3 MiB | 145.3 MiB |
| Added private resident memory for folded results | 73.2 MiB | 6.6 MiB |
| Private committed memory with folded results | 368.1 MiB | 384.7 MiB |

Both native runs completed with no captured JavaScript errors. These are synthetic fixture measurements, not a guaranteed saving or speedup for every conversation. Absolute working-set and commit measurements vary with runtime/OS allocation and differ already at startup; the card-generation timing excludes later layout/paint. WebView2’s underlying browser/renderer/GPU process overhead remains.

The diagnostic attempted to expose browser GC, but the installed host did not expose `window.gc`; native WeakRef counts therefore do not establish collection. A separate Node/V8 fixture loaded the actual before/after translation source with `--expose-gc` and DOM-owner relationships. It retained 80/80 removed panel objects before and 0/80 after. This verifies the strong-reference fix, not a browser RAM percentage.

## Validation

- All 43 JavaScript UI tests passed: queue transitions, transcript paging, exact expanded output, already-open results, error diagnostics, 27 locale catalogs, drafts and worker steering.
- Full Go tests passed for `internal/tools/processsession`, `internal/tools/search`, `internal/llm/providers` and `internal/agent`. Full `internal/webgui` tests passed with native Windows TEMP paths. The first broad run was interrupted by the pre-existing ancestor-fixture separator loop; additional WebGUI fixture comparisons also required native TEMP separators.
- The imported discovery and process-completion behavior passed focused end-to-end tests. Named discovery completes with one discovery call in both native and thin modes; process continuation preserves success/failure stdout without rediscovery.
- Windows CLI and WebView2 GUI builds completed. Production executables include these changes; Sciter remains an isolated experiment.

Microsoft recommends optimizing web content and retained DOM/event references for WebView2 memory issues: [performance guidance](https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/performance). Hardware acceleration, live streaming and the OpenCode Zen provider path remain unchanged. All application and fixture data remain under the application folder.

Raw fixture outputs and launcher source are kept locally in `.tmp/webview-memory-2026-10-01/`.

## Startup follow-up

The reported approximately 200 MB was immediately after opening the GUI, so this follow-up separates startup from folded transcript growth. A fresh native WebView2 fixture measured about 164 MiB private resident memory with empty data and about 140 MiB with copied pricing/session/settings metadata. These separate runs vary substantially, and the fixture intentionally used the echo provider rather than the user's configured providers. Its live Go heap was approximately 3-4 MiB; it does not establish the memory of the user's complete configuration or promise a 200 MB-to-140 MB reduction. The existing runtime process overhead remains. No production GC loop, working-set trimming or GPU disabling was added.

Two avoidable startup costs were removed:

- NewEngine installs rates from the pricing entries it has already loaded instead of reading and decoding the same cache again. Freshness rules and provider-specific cached-input prices stay unchanged.
- Model metadata remains cached, but a closed model picker creates no model-row DOM. Opening renders from that cache immediately, and closing releases the rows and their listeners. Async refresh after closing keeps the picker unmaterialized. The active model/context control still updates, and search, selection, hiding and setting a default remain available.

A native before/after fixture with 1000 synthetic cached models observed 8000 model-list elements before and 0 after while closed; both built all 1000 rows when opened. Closing retained 8000 elements before and 0 after. The synchronous closed-picker render took 10.4 ms before and 0.2 ms after. Private resident process-tree memory was 146.9 MiB before and 139.8 MiB after initially, then 167.9/160.2 MiB after closing. These absolute differences include runtime variation and are not an isolated RAM guarantee. Both runs captured no JavaScript errors. Detailed results are startup-palette-before.json and startup-palette-after.json in the local fixture folder.

Final validation: all 45 UI tests; full internal/account/pricing, internal/webgui and internal/agent Go tests; go vet on the same packages.
