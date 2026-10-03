# R14: visible orchestrator search focus

Baseline: 78c536688ab39f1ed062dee56690bfd29ce7c5a1 (R13/dev16). Status: approved exact production port; full UI integration pending.

## Concrete defect and correction

fill constructs search/list beneath pop.hidden=true and attempts search.focus before the caller unhides the popup. The native browser ignores the hidden input focus. The minimal candidate returns the already-created input from fill, captures it only on open in a callback-local variable, retains the existing visibility toggle, then focuses that input. Closing performs no fill/focus. No additional DOM query, persistent field/cache, listener, timer, option or network call. Generic outside-click, search-list and save callbacks are unchanged.

## Evidence

A hidden-ancestor-aware deterministic fixture models browser focus. Existing registration-order, generic picker/save and 100 materialized-list GC regressions pass on both versions. The new visible-search regression fails on baseline at opening must focus the visible input; all four tests pass on candidate. New regression covers initial/open/reopen exact-input focus, immediate filtering, close without rebuild, persistent button identity and zero extra saves. Results: test-results.json.

Native isolated Chrome, using an existing bundled runtime and separate portable profile/temp/cache/crash entirely under this ignored R14 folder, confirms:

| Open after rebuilds | Baseline | Candidate |
| --- | --- | --- |
| 1 | BODY; search not focused | INPUT; exact visible search focused |
| 100 | BODY; search not focused | INPUT; exact visible search focused |
| real keyboard types model-18 | input remains empty; 20 rows | input is model-18; 2 rows (default + match) |
| second connected picker opens | first closes, second opens, no search focus | first closes, second opens, second search focused |
| GC after 100 materialized opened/filtered rebuilds | 1 listener, 1 picker, first detached=false | 1 listener, 1 picker, first detached=false |

Opening both arbitrary hosts, inside/outside click behavior, active-choice no-save, provider-ref saves, language labels and button/wrapper identity all match apart from intended focus fields. Copy bytes match exactly for Unicode, indentation, newlines and HTML-looking text. Copy leaves the observed active element unchanged within each version (baseline BODY, candidate INPUT); no clipboard used outside the synthetic local page. Full final serialized HTML hashes match for both sizes: one rebuild 937bb0eb28d9ed6b1bf119854c779cb26af6ac23b0bc7c1c628c3304f6ff1887;100 rebuilds efad13bc6a41bffea1604623e4d03b98d04a49888c7706f6660af9cccb06644a.

The fixture aborts all network routes and uses synthetic setContent and local inline sources only. It does not use user applications, windows, tabs, profiles, sites, models or settings. The own headless browser context closed in finally; fixture process exited0, no live handle. No timing benchmark was repeated and no native RSS/TPS/latency saving claimed. Results: native-results.json and native-run.txt.

## Remaining scope and limits

Production source was confirmed byte-equal to the baseline immediately before applying the measured overlay. The narrow change in 07-stats.js returns the created input and focuses it after visibility toggles. The existing stats-picker-lifecycle fixture now respects hidden ancestors and includes one meaningful open/reopen/filter regression. The full UI suite is run by root during integration after this file freeze. The already-proved R13 generic query tradeoff is unchanged and is documented separately; this correction adds no query.

All explicit current GUI .focus calls were statically inspected. No other equally concrete focus-before-visible pattern was found: model/reasoning menus apply visibility before deferred focus, and direct dialog/provider/goal inputs append before focusing. No patch is proposed for them. TUI component focus is a different logical API, so this browser failure does not demonstrate a TUI defect.
