# Folded live tool input — 2026-10-02

Base: ab2ebb9 (dev.9). Live tool outputs and saved rows already rendered their large payloads only when opened. Live input still called prettyJSON and built hidden label/pre nodes immediately. Task and continuation rows then discarded that input for their brief.

The input path now uses the existing renderToolPayloadWhenOpen helper. Task/send_message skip that unused formatting; read_lines/read_context/read_many retain their input-free path. Opening a running row still shows the input immediately. Input stays before output after success or error, and reopening preserves the same nodes. Original arguments, status/clocks, worker activity and media previews are unchanged. No timer, retained cache, provider instruction or dependency was added.

Readonly aggregate saved-session metadata grounds an isolated generated replay: 1,349 calls, 1,193 eligible inputs and 2,071,240 argument characters. No identifiers or raw private content were exported. In the dependency-free DOM fixture:

| While every tool row stays folded | Before | After |
| --- | ---: | ---: |
| Input prettyJSON calls | 1,193 | 0 |
| Formatted input characters | 2,088,831 | 0 |
| Elements | 11,830 | 9,444 |
| DOM nodes | 18,263 | 13,491 |

All rows have identical tree/text/order after expansion. The A-B-B-A synchronous replay measured 67.53/49.51 ms before and 46.77/40.02 ms after; cold/order variability prevents a precise native latency claim. These counts do not measure WebView2 RSS, browser retained heap, FPS, image decoding or provider speed. The original argument strings remain available; the saving is deferred formatting and unrequested hidden DOM.

Five new permanent regressions cover folded and already-open inputs, callback ordering, raw/Unicode/malformed/empty arguments, stopped/error rows, file reads and task/continuation briefs. Three fail before the change. All 17 focused tests and 161 full UI tests pass after the port; owned diff check passes. No Go/model/native-app call was needed for this frontend change.

Evidence and limits: .tmp/goal-ui-round7-2026-10-02/REPORT.md plus metadata-only queries, source overlays, exact-output replays and test receipts. Full application integration belongs to the main agent.
