# Smarter operation grouping

## Scope and production behavior

The user's chosen thinking mode and effort remain unchanged. This change guides operation selection at that same setting; it does not lower reasoning, introduce a second planning model, or stop a run before all requested results and checks are complete.

The universal core explicitly asks for independent calls in one turn, prefers batch tools and preserves the ordering of dependent steps. Existing outcome/evidence, known-path, verification and completion instructions remain. The core is 1,195 bytes versus the previous 1,197, within its existing 1,200-byte budget.

The `read_lines` descriptor describes a single range and points multiple independent reads to `read_many`. The `read_many` descriptor explicitly includes verification and preserves concurrency, request syntax, globs and bounds. The pair adds 26 descriptor bytes. Their execution, schemas, argument validation, authorization and result semantics are unchanged.

Existing parallel execution remains responsible for independent read-only tools. Arbitrary commands remain ordered barriers unless their trusted contracts establish independence. Calls already grouped in one model turn do not save another model request merely by using a batch tool, and actual tool counters continue to count real invocations. There are no website-specific rules or automatic call rewriting.

## Broader regression coverage

`internal/agent/action_batch_regression_test.go` adds native and dispatcher variants with arbitrary inspection/effect names. Real filesystem mutation remains between two concurrently executed service/runtime inspection groups. A partial effect which fails still invalidates reusable evidence, so subsequent inspections observe the new revision and preserve the original error. The tests assert actual execution, verification, call/result ordering, UI event pairing and completed public runs. All four focused variants passed.

Existing suites cover multiple download destinations, path aliases, discovery/dispatcher ordering, local/cloud delegation, repeated instructions to a worker, cancellation, started-operation completion and continuation. These checks remain required.

## Live comparison protocol

The opt-in `internal/webgui/agent_operation_broad_live_test.go` exercises the real GUI request path with the local Qwen model, ordinary automatic delegation and the same maximum thinking setting before and after. Each run uses portable isolated workspace/data directories under `.tmp`; normal package tests skip live inference.

The new scenarios run two installed local Node CLI checks, then find an initially unknown active configuration, change one requested value and run an actual CLI verification. Unrelated fixtures and verifier programs must retain their original bytes. Actual successful command outputs and dependency order are checked, along with final configuration values, grouping, repeated successful calls and work after completion. The existing general scenario separately checks facts from three sources, two requested output formats and required read-back of both outputs.

The baseline test binary was frozen before production edits. Candidate and baseline use byte-identical fixture contents and user prompts, with sequential model requests and no competing heavy root checks. Single real-model samples are observational; timing varies with model sampling and backend conditions.

## Final live results

| Scenario | Tool calls before / after | Model calls before / after | Seconds before / after |
| --- | --- | --- | --- |
| Independent CLI checks | 2 / 2 | 2 / 2 | 33.137 / 36.356 |
| Find configuration, edit and real CLI check | 6 / 5 | 5 / 5 | 33.280 / 37.376 |
| Facts from three sources | 3 / 3 | 2 / 2 | 43.992 / 41.984 |
| Two outputs and required read-back | 4 / 4 | 3 / 3 | 34.934 / 38.845 |
| Total | 15 / 14 | 12 / 12 | 145.343 / 154.561 |

All outcomes and required verification passed. The saved operation was reading the code of the already requested CLI verifier before editing; the candidate ran that verifier directly after the edit. Independent CLI calls were grouped in one response in both variants. Both variants still used three separate source reads and two separate verification reads: there is no demonstrated improvement in batch-reader selection in this sample.

The candidate was 6.3% slower in observed total wall time. Input tokens were 68,371 before and 69,166 after; reasoning tokens 974 and 1,011. This is not evidence of a speed or total-token reduction. The small advisory changes are retained alongside regression coverage, with their limitation explicit. Production execution semantics are unchanged and grouping remains a model choice.

Both variants had zero tool failures and exact repeated successful calls. The two broad scenarios had zero ordered results after their completion evidence. That metric includes later members of the same call batch, but transcript result order is not a measure of physical completion order for parallel tools. Raw repeated successful calls are reported separately and would not automatically imply unnecessary work after a state change.

Every request in each of the four turns had the same ordered native-definition hash for its variant. The two requested outputs and their actual required read-back passed, as did the facts, configuration values and unchanged checker/example bytes. The broad validator requires a genuine installed Node entry script and an explicit successful exit code, recognizes lowercase errors, decodes the edit destination, and accepts ordered edit/check results within one model response. Focused validator tests and identical final offline validation of both frozen transcripts passed. Shell variants outside its finite supported argv shape require offline review.

The complete comparison and source/fixture equality checks are in `.tmp/smart-operation-fix/comparison.json`, generated by `compare.cjs` from completed receipts:

- Baseline broad: `.tmp/smart-operation-fix/runs/run-1637183010/receipt.json`.
- Candidate broad: `.tmp/smart-operation-fix/runs/run-2453871772/receipt.json`.
- Baseline general: `.tmp/agent-operation-live/run-392771034/receipt.json`.
- Candidate general: `.tmp/agent-operation-live/run-3378648083/receipt.json`.

## Final checks and installation

The complete final test run passed for `internal/agent`, `internal/llm/prompt`, `internal/tools/files`, `internal/tools/search`, `internal/tools/workflow`, `internal/tools/web`, `internal/app`, `internal/ui/tui` and `internal/webgui`. `go vet` passed for the same packages. Both CLI and GUI builds succeeded. Logs and timings are in `.tmp/smart-operation-fix/checks.json` and `check-0.log` through `check-3.log`.

Both installed executables match their staged SHA256 hashes, and each completed `--help` with exit code zero. Installation preserved rollback copies under `.tmp/smart-operation-fix/backups`; `installed.json` records replacement evidence and `help-smoke.json` records successful installed-binary checks. A redundant outer PowerShell native-exit-code condition initially reported failure after the PowerShell installer had completed; hash verification and the installed-binary checks confirmed success, without repeating replacement. `source-proof.json` records the final source/report hashes.
