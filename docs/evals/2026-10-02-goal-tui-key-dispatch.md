# TUI keyboard dispatch allocation — 2026-10-02

## Change

Keyboard events now go from public Model.Update directly to a private value receiver containing the existing key case. Non-key events keep the existing dispatcher. The general dispatcher retains a forwarding key case for compatibility. Terminal input batches still run in order, and draft recovery still runs after each individual event.

This removes one unnecessary heap allocation of the entire root model on ordinary keys. Compiler escape analysis traced that allocation to unrelated ask/menu paths: Bubbles Focus/Blur stores a pointer to an embedded textarea style. Because those paths shared the general event method, its receiver escaped even when processing a simple key.

Model and Palette APIs, value-copy behavior, focus/blur calls, renderer ownership, style callbacks and render caching are unchanged. No new application option, timer or model instruction was added.

## Measurements

Windows amd64, Ryzen 7 5800X3D, Go 1.26.2, GOMAXPROCS=2. Synthetic fixtures have no agent/provider and do not execute returned commands. All artifact/cache/temp writes were below repository .tmp; existing Go modules were read only.

Model is 46,080 bytes: Palette 21,536; textarea 11,616; menu 5,512; Marker 4,432; viewport 1,168. Lip Gloss Style is 552 bytes. A no-inline Model value round trip measured 2.723–2.768 us with zero heap allocations; interface boxing measured 14.340–15.056 us and one approximately 49 KB allocation. Value copies do not copy backing transcript strings/slices.

Matched source A-B-B-A, 2,000 iterations/run, two repeats; median of four observations per side:

| Event fixture | Before us/op | After us/op | Before B/op | After B/op | Before/after allocs |
| --- | ---: | ---: | ---: | ---: | ---: |
| Empty Esc | 27.469 | 17.548 | 98,392 | 49,240 | 8 / 7 |
| Rune + Backspace | 82.551 | 55.233 | 201,761 | 103,446 | 111 / 109 |
| Empty Esc, unchanged draft recovery | 40.232 | 28.277 | 147,552 | 98,400 | 10 / 9 |
| Status refresh | 31.978 | 32.086 | 98,304 | 98,304 | 2 / 2 |
| First Update key in fresh widget | 34.195 | 28.214 | 102,751 | about 53,598 | 96 / 95 |

Fresh-widget timing excludes construction and uses warm process/catalog/OS caches; it is not a cold process startup measurement. Status-refresh timings overlap, so no non-key time improvement or regression is claimed. The unchanged recovery fixture starts no save timer; ordinary draft saving is verified separately.

The 10,000-event alloc_space baseline profile attributed approximately half of allocated bytes to Model.update and half to Model.handleKey. The final candidate removes Model.update's flat allocation; approximately 99.5% is now attributed to handleKey. The saved allocation is 49,152 bytes per ordinary key, or 98,304 bytes per rune+Backspace pair. Cumulative allocation profiles are not RSS. These tests do not measure live RAM, terminal input-to-pixel delay, GUI layout/FPS or provider latency.

The final tracked BenchmarkTUIKeyDispatch confirms the same allocation counts/bytes: Esc 49,240 B; Esc with recovery 98,400 B; typing pair about 103,447 B; status refresh 98,304 B.

## Rejected approaches

A shared palette pointer was not introduced: independent style replacement on copied models, mutable renderer environments and stateful Transform callbacks are supported. The large style values alone do not establish safe immutable ownership.

Focusing/blurring a local textarea copy reduced allocation but failed an explicit regression: replacing input.FocusedStyle.Prompt after pointer endAsk no longer appeared in input.View. Baseline passed and prototype failed. Its smaller allocation did not justify changing style ownership.

An intermediate wrapper around all event dispatch was superseded because it added an unnecessary root-model value hop to the non-key path. The final change routes only keys in the existing public Update wrapper.

## Validation

Permanent tests cover AltGr versus ignored Alt keys, menu navigation and draft/focus retention, AskUser versus busy composer input, direct post-focus style replacement, stateful style transforms, independently replaced copied styles, Unicode/newline key batches, mixed resize/clear/text batches and saved/reopened draft recovery. Existing TUI tests retain the color/environment/cache and transcript coverage.

The overlay also compared the complete old switch with the new dispatch across en/pl/uk/tr, 40/100 columns, ASCII/TrueColor, mutable dark background, live transforms, folds, two queued asks, run end and status refresh. No old-switch duplicate is retained in production or permanent tests.

Final whole ./internal/ui/tui test passed (1.843 s); go vet ./internal/ui/tui passed. No EXE build, staging or commit was performed by this agent. Raw profiles, parity fixtures, ABBA measurements and rejected prototypes are in .tmp/tui-style-ownership-2026-10-02; direct-matched-results.json and direct-summary.json identify the final candidate data.
