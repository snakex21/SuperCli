# Controlled worker continuation window comparison

## Setup

The optional continuation evaluator can replay a verified initial stage from its saved synthetic fixture, executing the original assistant actions locally. Initial-stage model replies are scripted, not external calls. Independent Go tests and unchanged-test checks validate that replay before the live follow-up.

Before continuing, both arms restore the exact saved history through LoadConversation and mark the same active model. The initial follow-up message views, including their freshness tail in these runs, are identical. The seed history SHA-256 is d375eaeb0ded1b7eb05aa6444605c6b05ad694733568c96b74f656d8627ae964. Provider request snapshots now record effective pruning, tool names, and local estimates; this instrumentation is evaluation-only.

Only the synthetic fixture is sent to muse-spark-1.2-contributor-free. The normal command allowlist, independent checks, unchanged supplied tests and final-report requirement remain. Each follow-up is capped at 16 model calls. Native provider state and the special Zen request dialect are preserved.

## Invalid preliminary comparison

Artifacts a-16k and b-64k both actually used 16,384 tokens, despite requesting different limits. This exposed the missing legacy WindowFor inheritance described in 2026-09-27-worker-window-inheritance.md. Their 9- and 13-call results are not treated as a 16k/64k comparison. Both produced correct code, passing unchanged tests and final reports.

## Corrected pair

After fixing inheritance and adding an assertion before the first request:

| Arm | Verified window | Follow-up model calls | Tool calls | Input tokens | Worker duration | Outcome |
|---|---:|---:|---:|---:|---:|---|
| c-64k-fixed | 65,536 | 10 | 9 | 92,580 | 34.630 s | Correct, tests unchanged/pass, final report |
| d-16k-fixed | 16,384 | 14 | 13 | 151,656 | 51.709 s | Correct, tests unchanged/pass, final report |

The 64k arm pruned zero results on every request. It still discovered already-defined patch/command tools and, after a successful check, reread the edited file and ran go test ./... -v.

The 16k arm first contained 10 pruned result markers at request 7. Its subsequent calls included repeated discovery/reads and a second identical passing go test ./.... Stored-output references were present on archived results.

## Conclusions and limits

Pruning is not required for redundant discovery or verification to occur: the unpruned 64k trace demonstrates that directly. The smaller arm had more calls in this pair, but model trajectories differed even before pruning, and provider sampling is not controlled. This single pair does not prove that increasing a window will make real sessions faster, nor quantify a causal pruning penalty. No global context limit or pruning policy was changed on this basis.

The next investigation should isolate tool-use behavior with complete evidence available, rather than assuming the worker forgot the history. The confirmed inheritance bug is fixed independently.

Artifacts: .tmp/worker-continuation-window-2026-09-27/ and the original seed under .tmp/worker-continuation-replay-2026-09-27/muse/result.json.
