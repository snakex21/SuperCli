# Code-worker completion-hint comparison

The current coding replay showed redundant reads/checks after a correct edit. One candidate was the automatic 693-byte completion contract duplicated alongside the built-in code-worker system guidance. An opt-in SUPERCLI_CODING_OMIT_HINT=1 branch was added to the existing isolated live test; it does not alter production roles. Saved results now record both the requested variant and whether the hint actually reached user history.

All runs used the same cache-boundary fixture, thin protocol, deferred optional schemas, written patch snapshots, unchanged test requirement and independent verification. Trial order was Qwen with / Muse without, followed by Qwen without / Muse with. No private project content was sent to a provider.

| Model | Hint | Calls | Input tokens | Worker time | Correct code, checks and report |
|---|---|---:|---:|---:|---|
| Qwen 3.8 27B | present | 5 | 18,704 | 30.284 s | yes |
| Qwen 3.8 27B | omitted | 7 | 27,860 | 40.816 s | yes |
| Muse Spark 1.2 free | present | 7 | 31,288 | 12.375 s | yes |
| Muse Spark 1.2 free | omitted | 8 | 33,931 | 12.973 s | yes |

No command-gate rejections occurred. The shorter Qwen run's extra calls were a whitespace patch failure, a source reread and a corrected patch. The shorter Muse run still rediscovered patch_file and reread an already-available source excerpt. These observations do not establish prompt causality or a general speed ranking: they are one task/pair per model with uncontrolled serving latency.

Decision: do not remove the implementation hint from code workers. The test did not demonstrate a task-level benefit, and the current production prompts remain unchanged. The test hook is retained for explicit evaluation only. The new Qwen failure provided fresh, independent evidence for improving whitespace failure diagnostics instead.

Artifacts: .tmp/worker-hint-ab-2026-09-27/{qwen-1-with,qwen-2-without,muse-1-without,muse-2-with}/deferred.json.
