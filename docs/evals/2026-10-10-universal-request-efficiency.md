# Universal request and worker handoff efficiency

Date: 2026-10-10. The scope is shared agent/backend behavior, not special handling of Qwen or any download site. User-selected reasoning effort, thinking toggle, provider/model selection, output budget, routing decisions and conversation facts remain unchanged. Temporary files, caches, test fixtures and executable backups remain inside the repository/application directory.

## Exact tool estimate carried with the request

The existing definition snapshot already computes an estimate when its tool set changes. Main completions previously discarded that value and scanned the same provider-owned definitions again. The loop now prepares one pair: the owned definition slice and its matching estimate, and carries both into completion.

This pair refers to the exact prepared definitions, not the current registry. A late registration/activation cannot relabel an earlier request's cost. On the uncommon context-overflow retry, the same provider-owned slice is repriced because an adapter may have normalized it before rejecting the first request. No request content, tool availability or provider invocation count changes.

Tests cover native/thin tools, all four route modes, hidden/native/retained reasoning, images, resolved tool evidence and final-only state. Cold/warm preparation is compared with the original exact definition builder and full request estimator. Canonical history stays byte-identical. A completion test changes registry revision after preparation and checks actual provider-received definitions and calibrated tokens. An actual loop retry test mutates the adapter-owned descriptor before a context error, verifies the retried request's matching estimate, and confirms registry contracts remain intact.

The initial retry fixture assumed exactly one exposed schema; normal loop preparation also exposes a built-in schema. The corrected test finds its registered target by name and accepts the existing tool set, rather than changing production tool availability to satisfy that assumption.

Measurements and verification receipts are recorded under `.tmp/universal-request-efficiency/`. They measure local preparation CPU/allocation, not model-generation or end-to-end session latency. Removing this redundant scan does not reduce billed/model tokens.

| Exact schema preparation/estimate | Previous median | Current median | Allocated bytes, both |
| --- | ---: | ---: | ---: |
| Native, warm snapshot, 63 definitions | 4.922 microseconds | 0.611 microseconds | 3,072 |
| Native, snapshot miss | 13.660 microseconds | 9.695 microseconds | 15,616 |
| Thin, warm snapshot, 15 definitions | 0.953 microseconds | 0.208 microseconds | 768 |
| Thin, snapshot miss | 6.574 microseconds | 5.854 microseconds | 13,336 |

Three 250 ms samples per case compare previous/captured paths in the same process. Slice ownership/allocation is unchanged; the saving is the redundant schema scan. Receipts: `bench.json`, `bench.log` and `focused.log`.

## One completed worker handoff shared by the UI and model

Synchronous and background completion previously assembled the same notification twice and captured several snapshots. The completed invocation now captures its snapshot and historical evidence together under one read lock and shares the immutable notification/summary with the UI and tool result. A later worker continuation obtains a fresh handoff. It does not reuse old task results or skip provider calls.

The independent, frozen pre-edit implementation is a byte oracle for 45 combinations, including report/evidence sizes, failure and cancellation. Tests preserve Text, RetainedText, ModelPreview, recovery cause, steering and checkpoint behavior. Scripted end-to-end worker tests preserve exactly two provider calls.

The final parent benchmark uses a 256 KiB report, CPU16, five 500 ms samples with no other benchmark running. Median time is 274.007 → 217.735 microseconds; allocations are approximately 1,358,170 → 814,559 bytes and 90 → 44 per handoff. All five prepared samples are below all five legacy samples. This is the local report handoff, not model or tool execution time.

Earlier agent runs at CPU16 showed inconsistent latency, including regressions for large reports. They remain in `.tmp/universal-delegation-audit/receipt.json`; a reversed-order CPU1 control and the parent CPU16 run did not reproduce that regression. No explanation such as GC is asserted. The consistently lower allocation count is the strongest conclusion. The candidate is accepted with these limits. Parent log: `.tmp/universal-request-efficiency/handoff-bench.log`.

## Cache capability and first-turn correctness

The cross-provider audit found a concrete native Anthropic omission: with a changing trailing reminder merged into the only user message, no earlier message could carry a cache marker, so the first request had none. The fallback now marks the existing stable system as a single text block. It preserves the instruction text, tools, budget and canonical signed prefix. Regular later-message placement, compatible gateways and one-shot auxiliary requests retain their existing behavior.

The cache boundary follows the supported tools → system → messages ordering documented by [Anthropic](https://platform.claude.com/docs/en/build-with-claude/prompt-caching). This is an API capability change, independent of model names. It adds 62 wire bytes only in the missing-marker case. A reconstructed pre-fix overlay compiles and fails the new regression; the fixed transport tests pass. Four actual serializers/parsers with synthetic transport report the same 1,000 input / 750 cached / 2 output / 250 evaluated counts. No live inference, configuration change or billed API call was used.

Stable request bytes, cache hints and keys alone do not prove a cache hit. The common zero-cached value also does not distinguish missing provider reporting from a miss. The audit therefore does not claim faster local first compaction. Existing local cache_prompt hints, stable tool snapshots and per-instance Responses keys remain in place; arbitrary slot pinning or model heuristics were not added. Receipts: `.tmp/universal-cache-audit/audit.json` and its before/fixed logs.

## Reported GUI speed regression

The user reported that the statistics sidebar no longer displayed generation speed or the conversation average. The persisted last-turn event has no generation rate. After refresh/resume it overrides the live event, and the last-turn renderer hides the speed row when the field is absent. The durable usage journal already stores duration and time to first output; the stats visitor had omitted those columns.

The intended correction is a clearly labelled weighted session rate: sum of reported output tokens from measured calls divided by their matching sum of measured stream time. It includes the session's different models and helper purposes, not just the selected model or latest answer. Reasoning is a part of reported output and is never added twice. Unknown clocks and unsuccessful calls are not manufactured into rates. A session without measurements should show an unknown value, while existing measured samples survive an unmeasured latest answer. The generation-speed preference remains authoritative.

Implemented via three existing timing columns in the existing streaming stats SELECT, with no extra query, database migration or timer. The popup and sidebar share the same stream validity gate. The UI keeps a separately labelled last measured live response only for the same active session when durable measurements are unavailable; it does not assign that rate to a newer unmeasured response or another model. The 27 locale catalogs have complete matching keys.

The first broad check passed 18 of 19 Go targets, but the webgui command-completion integration failed: its deliberately isolated production chat module does not load the stats module, and the new unconditional done-hook threw before normal stream completion. This is a real module-boundary regression in the candidate, caught before installation. The completion/active-work assertions remain intact; the stats hook must be optional for chat completion. Raw failure remains in `go-tests.log`.

The hook is now guarded by its function availability. A production-handler test covers no module, the actual stats module and a nonfunction value. The unchanged actual HTTP command-completion integration passes, including native success/error/checkpoint rejection, EOF, idle ownership and immediate next submission. Full webgui retry passes in 59.017 seconds. The full Node run then exposed two extracted-handler fixtures without the real chat module's activeSessionID global; one harness field fixes this with no production change or weakened source/cleanup/order assertions. Final Node result: 309/309 PASS, no skips or failures.

## Visible GUI and final installation

An isolated portable echo fixture stores two measured calls from different models/purposes, 130 output tokens over 8 measured seconds, followed by an unmeasured main reply. The sidebar shows 16.3 tok/s; after browser reload and reopening the same saved conversation it still shows 16.3. Selecting a different unmeasured conversation shows an explicit dash and does not inherit the previous rate. This is example data, not a benchmark of a real model.

At a 640 × 900 viewport with Georgia and 140% UI scale, the sidebar has no horizontal overflow (client/scroll width both 456 CSS pixels). No browser console errors were reported. Screenshots: `.tmp/universal-request-efficiency/visual-speed/session-average.jpg` and `unknown-average.jpg`. The viewport override was reset, the created browser tab closed and only the owned test process stopped.

Final validation: all 19 Go target results pass across the initial run and the corrected full webgui retry; 309 UI tests pass; go vet passes for all 19 targets. The first failed logs remain available rather than being overwritten with successful output. The final UI receipt is `.tmp/universal-speed-panel/receipt.json`; consolidated receipts are in `.tmp/universal-request-efficiency/checks.json`.

Both executables were rebuilt with source hashes stable before/after build and atomically installed with verified backups. Installed --help smoke tests pass for both. No Git commit or push was made. Restart a running application to use the new executables.

| Installed executable | SHA256 |
| --- | --- |
| supercli.exe | B7344556DC000864D8190FE5B68DB235FB30742D1D8D73A54B9460C6F9AB7679 |
| supercli-web.exe | 034ED80BDD26067F423BF9D918D917685DEAD50F4424046B83855B2878A56531 |

Previous executable backups: `.tmp/universal-request-efficiency/backups/27625074-0b2e-4b72-81fb-795071b99451/`. Installation, hash and smoke receipts: `installed.json`, `build.json`, `installed-smoke.json`.
