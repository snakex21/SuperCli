# Worker report handoff: live replay — 2026-09-26

## Scope

An opt-in test sends a synthetic completed worker report through the real coordinator Loop and registry output store. The fixture has nine findings with priorities and a final NOT_RUN verification status. Its long introduction and evidence paragraphs reproduce the information-loss shape seen in the saved GunMayhem report; no private session content is sent to a provider.

The only available tool is read_output. Models cannot execute commands, modify files or inspect the user's projects. This measures interpreting an already completed report, excluding the cost of the worker's review and subsequent implementation.

The legacy arm uses the old generic beginning/end preview. The outline arm uses the production bounded heading outline. The full report is available in both arms. Providers are the existing LM Studio OpenAI-compatible route and the existing Zen Responses route; their production settings and transport were not edited.

## Observations

Each answer retained all nine finding IDs, their priorities and NOT_RUN. Early trials automatically checked IDs/status; priorities and descriptions were also inspected. The current evaluator checks the priority on each finding's line as well. This is a small exploratory sample, not a statistical performance benchmark.

Calls means coordinator model calls. Reads means executed read_output calls. Input/output counts are summed provider usage for the trial. Time includes the coordinator turn only.

| Model / route / order | Preview | Calls | Reads | Input tokens | Output tokens | Seconds |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Qwen / native / legacy first | Legacy | 3 | 2 | 8,485 | 824 | 27.67 |
| Qwen / native / legacy first | Outline | 1 | 0 | 1,465 | 610 | 18.74 |
| Muse / native / legacy first | Legacy | 3 | 2 | 8,954 | 668 | 8.10 |
| Muse / native / legacy first | Outline | 2 | 1 | 5,078 | 632 | 5.29 |
| Qwen / native / outline first | Legacy | 3 | 2 | 8,486 | 825 | 27.83 |
| Qwen / native / outline first | Outline | 2 | 1 | 3,709 | 1,087 | 32.18 |
| Qwen / thin / legacy first | Legacy | 3 | 3 | 12,177 | 966 | 34.34 |
| Qwen / thin / legacy first | Outline | 2 | 1 | 5,139 | 1,095 | 33.79 |
| Muse / thin / legacy first | Legacy | 3 | 2 | 9,571 | 734 | 6.72 |
| Muse / thin / legacy first | Outline | 3 | 2 | 11,062 | 1,382 | 12.81 |

The outline reduced calls and input in several trials, but not universally. Qwen's reversed-order outline trial generated more output and took longer despite fewer calls. Muse's thin outline trial used the same number of calls and more tokens/time. Warm cache, generation length, model choices and provider variability prevent general wall-time claims.

A temporary test-only wording variant explicitly labeling the outline's completeness did not help: Muse made 3 calls / 2 reads (10,007 input, 814 output, 7.96 seconds), versus the original outline's 1 call / 0 reads (1,699 input, 502 output, 4.28 seconds). The candidate was removed. No extra production instruction was added.

## A separate execution bug uncovered

The Qwen thin legacy trial executed the same read twice. Wire capture confirmed that one response contained both a native call and its textual sentinel mirror, with equivalent numeric/string arguments. The [separate fix and before/after verification](2026-09-26-mirrored-read-recovery.md) remove that duplicate execution and context insertion. The table above predates this fix, so some apparent route differences include that bug.

## Reproduction

Test: internal/agent/worker_report_preview_live_test.go, TestWorkerReportPreviewAB_Live.

Opt-in environment:
- SUPERCLI_REPORT_URL: http://127.0.0.1:1234/v1 or https://opencode.ai/zen/v1.
- SUPERCLI_REPORT_MODEL: qwen3.8-27b-uncensored or muse-spark-1.3-contributor-free in these trials.
- SUPERCLI_REPORT_OUT: an absolute directory inside the application's .tmp folder.
- SUPERCLI_REPORT_ORDER: legacy,outline (default), outline,legacy, or one arm.
- SUPERCLI_REPORT_ROUTE: thin for text tool routing; omit for native.
- SUPERCLI_REPORT_TRACE: 1 to retain native calls/text protocol data for this synthetic fixture.

Run: go test -count=1 -timeout=7m ./internal/agent -run '^TestWorkerReportPreviewAB_Live$' -v

Each arm has a 180-second context and at most six loop steps. LM Studio used temperature 0, seed 20260926 and max output 2048; reasoning effort low. Zen used its existing Responses provider and low effort. Earlier native trials allowed four loop steps; none reached that limit.

Artifacts are written even if a provider returns an error or the answer fails evaluation. The test process exit status alone is not a model-quality assertion: inspect Error, Correct, PrioritiesCorrect and the answer. Without the required environment variables, ordinary test runs skip the live replay and make no provider calls.

Results and command logs: .tmp/worker-report-live-2026-09-26/. summary.json includes all trials, including the rejected wording experiment and the later mirror-fix runs. These local artifacts are not committed.
