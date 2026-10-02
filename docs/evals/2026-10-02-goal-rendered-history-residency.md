# Rendered GUI history residency

## Change

After a saved transcript page is successfully appended to the GUI, its rendered rows own the source they need: complete lazy tool payloads, reasoning, attachment handles, rewind text and unresolved boundary-call messages. The separate loadedTranscriptMessages array is not read again in production. It previously remained resident and received every older page through repeated concatenation.

08-sessions.js now clears that array immediately after successful initial construction and does not concatenate older pages into it. Initial rendering failure keeps the page for recovery. Existing pending boundary entries still retain their necessary message until an older call resolves them. No visible rows, raw lazy payloads, persisted history or media descriptors are removed, and no new cache, timer, service, model call or dependency is introduced.

The port was checked against the audited dev10 baseline b75443591265e8567031167a78b98d38872d70e7 before editing; the current 08-sessions.js SHA-256 matched the fixture source exactly.

## Evidence and limits

The portable ignored fixture in .tmp/goal-ui-round8-2026-10-02 runs current production GUI functions under the dependency-free DOM double in Node v24.15.0. Generated tool-heavy histories use 60-message pages. The largest 2700-message shape approximates the earlier anonymous count of 1349 actual tool calls, but its content and byte sizes are generated; no private message text was copied.

Releasing only the redundant array after building the same DOM produced these observations after explicit GC:

| Messages | Message wrappers before / after | Tool-call wrappers before / after | Concatenated array reference copies avoided | Observed V8 heap decrease |
|---:|---:|---:|---:|---:|
| 60 | 60 / 0 | 30 / 0 | 0 | 1480 B |
| 600 | 600 / 0 | 300 / 0 | 3240 | 69816 B |
| 2700 | 2700 / 0 | 1350 / 0 | 62040 | 927664 B |

The same tool rows remained attached and their full input/output bytes remained available on expansion. Object collection demonstrates removal of the redundant ownership. Heap deltas include fixture, GC and allocator effects; they are not an exact application memory guarantee. The raw result strings required by the DOM remain owned. This is neither transcript truncation nor a claim about native WebView2 RSS, image decode memory, FPS, input latency, provider prefill or tokens per second.

An initial diagnostic fixture replaced the contextified global from outside the VM and left V8's native-context extension binding intact. A synthetic heap snapshot identified that retainer; the final measurement replaces the global inside its VM. Debug artifacts are retained separately and are not the reported memory comparison.

## Verification

The complete Node UI suite passed 162 tests in 1.013 s after the production port. Regressions retain full lazy result bytes, initial render failure/retry, older-page ordering and scroll anchoring, focused image/source identity, expanded-output identity, worker backlinks, stale/failed request recovery and separately appended live rows. Boundary coverage includes unresolved calls across several pages and reused IDs binding each result to its nearest preceding call.

The older full-rebuild comparison explicitly preserves its historical raw-message array inside the test harness. Its existing 3300 versus 600 image assignments still compare the historical algorithm with incremental paging; the new release does not silently change that control.

Artifacts: .tmp/goal-ui-round8-2026-10-02/REPORT.md, history-retention.cjs, history-retention.json, overlay-parity.json and production-full-ui.json. No provider, user application, screenshot or GPU workload was used.
