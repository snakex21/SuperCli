# Bounded written snapshots from patch_file — 2026-09-26

## Evidence and scope

A fresh isolated coding task on Qwen and free Muse reproduced the remaining post-edit reread on Muse. The task fixes an expiry boundary in a synthetic Go cache, runs existing tests, and reports the result; an independent test run checks correctness and verifies that test files were unchanged. No user project content was sent to either provider.

Qwen already completed with five model calls and no reread. Muse first used eight calls, including read_context on the edited source after a successful patch and test. The successful edit result contained hashes but no written source. A second baseline also reread the edited source, repeated tests, and reached the eval-only eight-step ceiling despite correct code.

Artifacts are under .tmp/patch-result-preview-2026-09-26/. The initial current-code checks are under .tmp/worker-current-thin-2026-09-26/ and .tmp/worker-current-muse-spark-2026-09-26/. An earlier attempt used the incorrect model identifier muse-free and received HTTP 401 unsupported model; that setup error is excluded from comparisons. The actual free model is muse-spark-1.2-contributor-free.

## Implementation

PatchFile now derives a written snapshot from the final buffer already used for the write and its hash. No new disk read, model call, schema text or permanent instruction is added. The main loop and workers use the same result through native and thin invocation, in CLI/TUI and GUI.

- At most 1,024 bytes and 16 source lines per successful changed patch.
- Small files fit whole; larger files show context around the first difference with an explicit partial-snapshot marker. This is not a complete multi-hunk diff.
- Line numbers refer to the final written buffer. Chained replacements and relaxed line endings/indentation use actual written bytes, not requested replacements.
- Empty-file writes are explicit. No-op and failed patches add no snapshot.
- The result is a historical snapshot tied to after_hash, not a guarantee against a later external edit or formatter. It does not replace tests or prevent rereading when necessary.

## Live observations

| Trial | Model calls | Input tokens | Time | Independent code check | Post-patch source reread |
| --- | ---: | ---: | ---: | --- | --- |
| Muse initial baseline | 8 | 34,641 | 15.551 s | pass | yes |
| Muse second baseline | 8 | 34,805 | 16.563 s | pass; worker report hit step limit | yes |
| Muse preview prototype 1 | 7 | 30,648 | 12.719 s | pass | no |
| Muse preview prototype 2 | 5 | 20,024 | 8.983 s | pass | no |
| Muse production snapshot | 6 | 25,413 | 11.991 s | pass | no |
| Qwen initial baseline | 5 | 18,432 | 30.001 s | pass | no |
| Qwen production snapshot | 5 | 19,122 | 55.103 s | pass | no |

The two prototype trials used an eval-only post-write read to emulate a small complete snapshot. Production uses the already-written memory buffer and adds EOF/partial markers. The harness now has an optional SUPERCLI_CODING_PATCH_PREVIEW=0 baseline that only strips the snapshot from the result; normal runs exercise production behavior.

These are small, non-deterministic behavioral probes, not a broad latency benchmark. Muse consistently avoided the observed final reread; the final run used two fewer calls and about 26.6% less input than its initial baseline. Other exploration also varied between trials. Qwen did not save a call, input rose about 3.7%, and the observed runtime was longer; there is no demonstrated local-model speedup from this change. A bounded snapshot trades a little result context for the possibility of avoiding another request/read, rather than being a zero-token optimization.

## Local preparation and verification

Median of three 300 ms helper benchmark runs, with a change near the file end:

| Fixture size | Snapshot preparation | Allocated bytes |
| --- | ---: | ---: |
| about 300 B | 0.000666 ms | 504 |
| 100 KiB | 0.040950 ms | 1,105 |
| 1 MiB | 0.397597 ms | 1,106 |

This is added local formatting work, not the whole patch operation. No full-file line index or retained copy is allocated by the formatter.

Regression coverage checks exact and normalized writes, final chained state, empty-file changes, no-op/failure absence, EOF deletions, distant edits, long Unicode lines, byte/line limits, correct source offsets, explicit omitted content, and delivery to both model protocols and UI tool events. Initial red cases showed the missing output; focused tests and the full go test ./... suite pass. go vet ./... and CLI/GUI builds also pass. Evidence: red.json, focused-and-bench.json, suite.json, vet.json, build-cli.json, build-gui.json.

## Installation

CLI --help smoke passed. Both portable executables were replaced and their SHA-256 hashes verified against the builds; installed.json records hashes and backup paths. Existing processes were not terminated. A restarted CLI/GUI uses the new binaries. The efficiency goal remains active.
