# Qwen compaction timing, portable usage charts and currency history

Date: 2026-10-10. This evaluation continues the portable statistics and preflight work. It does not change the user's selected thinking controls or discard conversation facts to reduce latency.

## What the earlier minute measured

The earlier 60.046-second runtime included two requests: the compaction helper (38.845 seconds) and the subsequent main response (21.200 seconds). The helper's first model output arrived at 8.468 seconds; its total output was 1,080 tokens, including 653 reported reasoning tokens and 427 remaining output tokens. Thus most of that helper's measured time followed the first output. First output includes reasoning and is not necessarily the first visible summary text. The waiting interval also includes transport and backend scheduling; it is not a direct measurement of prompt processing.

Different histories, cache states and selected effort settings make unrelated runs unsuitable for a claimed before/after speedup. The current resolved effort must be preserved in the runtime fixture.

## Safe reduction in model work

Manual compaction now skips a prefix consisting solely of an existing, recognized saved compaction summary. This makes no helper request and leaves the summary, instructions and retained recent turns byte-for-byte intact. New user, assistant or tool evidence remains eligible for compaction. Quoted, attached, named and incomplete envelopes are not treated as this no-op. Emergency compaction behavior is unchanged.

No compaction output budget or reasoning level was lowered. The earlier native Git preflight optimization and removal of repeated summary envelope text remain in place.

## Durable measurements and simple GUI

Completed model calls save duration and first-output time alongside their existing usage record and immutable billing identity. This uses the existing writes; it does not add token-by-token writes, an inference call or a refresh timer. The token popup reads the existing durable journal on demand.

The popup charts reported reasoning as a subset of output, plus remaining output. Remaining output may contain unreported reasoning; it is not an exact split between prose and tool-call JSON. Unknown and partial reports are marked. Cache and reasoning are not added to the input/output totals a second time. A one-model view no longer repeats its tool-context estimate below the same detail.

Time details show measured waiting and streaming intervals, and separately attribute compaction in the purpose disclosure. Generation speed uses only output tokens matched to measured streaming intervals. Historical records, failed/canceled calls and invalid clocks do not manufacture durations or speeds. Sums of call durations are not elapsed session wall time when calls overlap.

## Currency history

The settings catalogue expands to 148 currencies from the official NBP A/B catalogue plus PLN. XDR is not included as an ordinary currency. The server provides the catalogue, and the GUI uses localized currency names with ISO-code fallback. The main statistics panel remains simple.

Existing frozen day snapshots remain intact. Newly supported currencies use additive, immutable per-day/per-currency quotes. Weekly table-B publication dates and the independent table-A USD reference dates are saved separately with their sources; the existing cost disclosure displays both dates when they differ. Missing or failed quotes remain explicitly unavailable instead of receiving a made-up conversion. Application data, fixtures, caches and backups remain inside the application/repository folder.

Official sources: [NBP API](https://api.nbp.pl/) and [NBP publication schedule](https://nbp.pl/statystyka-i-sprawozdawczosc/kursy/informacja-o-terminach-publikacji-kursow-walut/). Range requests fetch whole tables, at most 93 days per request, rather than making one request per currency. Previously saved quotes are not refreshed on a currency switch.

## Validation receipts

The isolated helper-only runtime used an existing completed coding fixture, without re-running its tools or asking for the main continuation. Offline validation required byte-exact equality with the fixture's original helper inputs after the existing production history projection: system prompt 870 bytes, transcript 25,996 bytes, 13 history messages. It produced exactly one HTTP 200 request, one completion and no retry.

Current resolved configuration and wire request preserved `xhigh` (global `high`, project override `xhigh`) with thinking enabled; configured zero maximum meant no `max_tokens` override. Native model metadata describes a thinking toggle rather than distinct backend effort levels. This diagnostic did not alter that behavior or the user's configuration.

| Helper measurement | Result |
| --- | ---: |
| Request through stream EOF | 12.885 s |
| First model output / first reasoning | 1.396 s |
| First summary content | 3.595 s |
| Input tokens | 8,180 |
| Output tokens, including reported reasoning | 411 |
| Reported reasoning tokens | 76 |

The visible summary retained nine checked facts: the quantity contract, the pending documentation step, no-publishing and no-dependency requirements, portable data requirement, protected files, the cause/fix and the before/after test result. These checks do not prove literal fidelity of every historical sentence: text marked as a quote was a paraphrase of an earlier instruction. No new reasoning contents were stored for timing analysis. This run generated fewer tokens than historical runs; it is **not** a paired before/after latency benchmark and does not establish a permanent threefold speedup. Backend cache metadata was zero and does not establish the cause of the observed waiting interval.

Receipt: `.tmp/compact-speed-oct10/runtime-1199663417/receipt.json`; assessment: `.tmp/compact-speed-oct10/assessment.json`. Focused compaction regressions: 92 top-level tests passed, three opt-in tests skipped without additional inference.

### Complete validation

- Go tests passed in 14 targets: FX, usage costs, session storage, LLM, provider factory, config, preflight, stats, GUI, TUI, app, agent and both commands (CLI command has no test files).
- `go vet` passed for the same targets.
- All 301 Node UI tests passed, including missing/partial timing, output-chart coverage, duplicate estimates, localized catalogue, historical currency dates/sources and all locale catalogues.
- Persistence tests cover schema upgrades, reopening storage, conversation deletion while preserving the durable journal, unknown historical times, canceled-call exclusion, correct compaction attribution and unchanged frozen prices. Normal/full backup tests preserve duration, first-output time and additional historical currency quotes.
- FX tests cover 148-code catalogue, unchanged old ten-currency snapshots, A/B quotes with independent dates, range batching, cache reuse, concurrency and explicit HTTP failures without manufactured rates.

Receipts: `.tmp/model-performance/checks.json`, `go-tests.log`, `ui-tests.log` and `vet.log`. Both executables were built from a checked stable source manifest (`build-sources.sha256`).

### Visual verification

An isolated portable GUI fixture used deliberately **example** token/timing data; it made no model request. With Georgia and 140% UI scale, the output chart, time chart and per-purpose compaction figures were readable. At 640 × 900, the dialog had equal client/scroll widths (404 CSS pixels) and the token table retained its own horizontal scrolling. The viewport override was reset afterwards.

After reload and reopening the saved session, totals remained 23,926, reported reasoning 756, remaining output 1,170 and measured call time 60.045 seconds. The model-detail tool estimate occurred exactly once. Settings displayed 148 currencies, correctly rendered the long localized BAM label, and persisted `cost_currency = "BAM"` in the isolated fixture. Browser error logs were empty. The test tab and only the isolated test GUI process were closed.

Receipts and screenshots: `.tmp/model-performance/visual-final/{visual-check.json,usage-chart.png,usage-timing.png,currency-settings.png}`. These screenshots demonstrate layout, not an inference performance benchmark.

### Installed build

Both application executables were replaced with verified copies and their previous versions preserved in `.tmp/model-performance/backups/0b7a5f22-a444-4465-b323-20c1092c2092/`. Installed `--help` smoke checks exited zero and verified the same hashes as the build:

| Executable | SHA-256 |
| --- | --- |
| `supercli.exe` | `C6CC69590FAF48BCA83CD70888FB153B70234951185DEC3E8ECC56653018FE41` |
| `supercli-web.exe` | `98EA0CBA6EDBA14F89B1D3750F18C1EB16ADCB04C4336A58ABD71489A29263E3` |

Installation receipts: `.tmp/model-performance/installed.json` and `installed-smoke.json`. User model settings were not edited. A running older application needs to be reopened to load the new executable.
