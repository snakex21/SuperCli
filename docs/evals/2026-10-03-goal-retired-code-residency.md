# Retired incremental code checkpoints

Date: 2026-10-03. Baseline: `6ab0f1cb20e6297444cf2378d678f5fa43917cbc` (dev.14).

The GUI keeps the complete assistant source and rendered code so completed blocks can still be read and copied. Its incremental Markdown state also kept the previous unfinished code checkpoint after the code node had been retired. This retained an additional earlier source buffer.

## Owner and change

`node._assistantParts` owns a Markdown stream state. `clearMarkdownTail` and the recovery/full-render reset set `code` to null but previously left `codeText` and `codeLang` intact. Their only production reads are in the active-code branch, which already initializes both before creating a new code node.

The change clears those two existing fields at both retirement sites: four assignments, no persistent cache, listener, timer, source truncation, or parser change. Active unfinished code still retains its checkpoint. The authoritative source, rendered code, and copy controls remain complete.

A generated one-block V8 heap snapshot identified both fields as sliced strings pointing to the same earlier 133,928-byte string. The current complete Markdown source used a different 133,968-byte string. Clearing only `codeText` was insufficient for `javascript-react`: `codeLang` still retained the earlier buffer. Clearing both removed that owner while preserving the current source. Short `js` labels did not retain the large buffer themselves in this fixture.

## Bounded measurements

The isolated Node 24.15.0 fixture loads the actual Markdown and transcript scripts using the existing dependency-free serialization DOM double. Generated ASCII code includes HTML-looking text; separate durable tests cover Unicode, indentation, later code, reset, recovery and render failures. Measurements keep completed nodes alive, collect V8 garbage, and compare complete HTML hashes. They do not open a browser, user application, or model.

For 20 generated blocks of 3,000 lines, the earlier code checkpoints totaled 2,707,780 characters. Separate processes ran baseline/candidate/candidate/baseline in the same test environment.

| Lifecycle | Baseline retained V8 heap (bytes) | Candidate retained V8 heap (bytes) | HTML parity |
| --- | ---: | ---: | --- |
| Closed fence followed by prose | 15,025,096–15,025,128 | 12,315,944–12,316,072 | All 20 node hashes identical |
| Replaced plain recovery source | 8,624,360–8,624,432 | 6,051,864 | All 20 node hashes identical |
| HTML-comment full-render reset | 8,406,696–8,406,760 | 5,697,272 | All 20 node hashes identical |
| Unfinished active code | 14,050,312–14,050,408 | 14,050,776–14,050,872 | Exact active checkpoints and hashes |
| Ordinary no-code/no-image/no-history text | 5,752,576 | 5,753,192 | Exact source and hashes |

Normal closure reduced the generated retained heap by 2,709,024–2,709,184 bytes. An additional smaller 8-block, 80-line fixture contained 27,432 earlier code characters and changed from 5,736,264 to 5,708,344 retained heap bytes, with all HTML hashes identical. Active and ordinary controls differed by less than 1 KiB, including the small source/metadata difference. They did not lose or shorten any text. A following small active code block already replaced the old checkpoint before this change and showed no material heap reduction.

These are generated component measurements, not observed user-session sizes or native WebView2 RSS, layout, FPS, input latency, provider TTFT, inference throughput or token savings. V8 may share some strings and retain internal buffers independently of these fields; field character counts are not universal freed-byte estimates. The full required source and DOM remain retained intentionally.

## Verification

Before the production port, 31 existing stream, first-paint and code-copy tests passed with the exact overlay. The durable regressions additionally verify completed-source retention, committed-node identity, later active-code identity, HTML-comment/replaced snapshots, and rebuilding after an injected render error. The production port passed the complete UI suite: 173 tests, zero failures (863.014 ms). The three new regressions fail against the original renderer and pass with the port. Existing code-copy tests continue to verify clipboard contents, indentation, Unicode, fallback, focus and selection.

Ignored reproducible evidence is in `.tmp/goal-ui-round12-2026-10-03/`: `code-checkpoint-retention.cjs`, `retirement-abba.json`, `small-closed.json`, `language-matrix.json`, `owner-graph.json`, heap snapshots, and scoped test receipts. The production script port is byte-identical to the measured overlay. No generated fixtures or raw histories are published.
