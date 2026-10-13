# Worker evidence, repeated reads and active-result pruning

Date: 2026-10-12. The changes apply to all providers and models. They do not select a different reasoning effort, output budget or sampling configuration.

## Findings and changes

Two independent mechanisms caused unnecessary exploration:

1. A fresh worker normally receives its task rather than the coordinator's existing source reads. In the live Haiku runs it read source again even though the coordinator had just read it. This occurred with small, fresh worker requests and no compaction; those observations do not establish context saturation as the cause. They also do not rule out saturation in longer, unrelated sessions.
2. The result-pruning policy could discard working evidence from the active user turn to meet a learned latency target, well below the model's actual window. The saved projection of the reported session included a pruned `read_output` result. A single shared archive with descending byte offsets is intentional; eligibility for pruning was the problem.

`task` now automatically attaches relevant source excerpts already read by its coordinator, up to 6,000 bytes and four files. Selection uses the delegated task and expected result, before incidental repository-preflight paths are appended. It uses only visible tool evidence, validates saved line hashes against current files, and rejects non-regular, oversized, changed, hidden, or out-of-workspace source. Workers without the corresponding source-reading tools receive no file evidence. Existing `share_context` behavior is retained; automatic source transfer does not copy the whole conversation.

Latency-only pruning now protects the ongoing user turn. Under actual context-window pressure ordinary bounded pruning remains available. Retrieved `read_output` evidence is protected throughout its current task, and becomes eligible again at the next real user turn. There is no permanent pin or second copy of its output. The normal user instruction boundaries exclude internal task notifications and tool image wrappers.

Unchanged-source references are recomputed from the actual outgoing view. Regression tests cover pruning, hiding, compaction and completed-result omission: when original evidence leaves the request, the next read contains the complete lines, and later references point to that visible replacement. Canonical source history is not shortened by source deduplication.

## Replay of the reported task

The session database was opened read-only. The portable trace contains source-reader call/result pairs from sequences 720–755 of session `c3269b923b40b594`; user messages, credentials and unrelated session content were not exported. Captured tools are never executed by the replay.

The regression uses a 128k hard window and an explicit 8k learned latency budget to exercise the faulty policy. These are controlled test conditions, not a claim about the exact live model window or historical budget at every request.

- Previous policy: failed at request boundary 7 after reclaiming 6,936 tokens of working evidence.
- New policy: 20 source results across 16 request boundaries, peak local estimate 19,074 tokens, zero working results discarded.

This reproduces evidence loss within the active task. The saved end-of-turn projection alone cannot prove that every fresh result was absent on its very first subsequent model request.

## Live cloud evaluation

The provider is the user's configured `clałd` connection. Models are `anthropic/claude-opus-5-5` and `anthropic/claude-haiku-5-5`. Configuration is resolved using the historical parent workspace. Every outgoing completion request is checked for `reasoning_effort=high`; a retry removing it is rejected. No reasoning setting or user session data was changed.

The task is a small Go cache-expiry boundary bug. Each run starts with a fresh isolated Git workspace and failing immutable tests. The agent uses the real Loop and coding tools, edits production code, and executes Go checks. A separate check verifies the fix, unchanged tests and unrelated files, a final report, and the requested presence or absence of delegation. No test-model edits target the real repository.

Runs are sequential with counterbalanced arm order. Go preparation uses portable temp/cache paths, `GOMAXPROCS=1` and `-p 1`. Concurrent activity elsewhere on the host and cloud scheduling are not controlled, so latency is noisy. Two repetitions per arm do not establish a universal speedup.

All 28 completed, non-calibration cases passed independent correctness checks. Every wire request retained `high`. Earlier harness calibration runs are retained separately and excluded from this count.

### Earlier source-efficiency changes, before automatic source handoff

| Model / execution | Before median | After median | Provider calls before / after | Tool operations before / after |
|---|---:|---:|---|---|
| Opus, main agent | 10.550 s | 12.446 s | 4, 4 / 4, 4 | 4, 4 / 4, 4 |
| Opus, delegation | 27.681 s | 23.633 s | 7, 7 / 6, 6 | 7, 7 / 7, 6 |
| Haiku, main agent | 12.550 s | 12.997 s | 4, 4 / 4, 4 | 5, 5 / 4, 5 |

The earlier Haiku delegation comparison is excluded from latency conclusions because ordinary formatting checks were rejected by an overly narrow evaluation command list.

### Automatic source handoff

| Model / repetitions | Before median | After median | Provider calls before / after | Tool operations before / after |
|---|---:|---:|---|---|
| Opus, r10/r11 | 22.535 s | 25.520 s | 6, 6 / 7, 6 | 6, 7 / 7, 7 |
| Haiku, r12/r13 | 41.555 s | 38.953 s | 11, 10 / 9, 11 | 14, 13 / 9, 11 |

In both final Haiku handoff runs the worker received existing source and began with `patch_file`, without the old initial `read_many`. Main-agent rereads and repeated checks still occurred. In the Opus handoff trials the parent had not read source before delegating, so no source was attached; discovery by that cold worker was necessary. There is no measured universal latency improvement.

The initial Haiku r10/r11 handoff comparison is retained but excluded from timing conclusions because `go test ./cache/` was rejected by the old evaluation allowlist. The final r12/r13 allowlist accepts that ordinary syntax. Their two before-arm failures were genuine model mistakes: a workspace-relative command directory duplicated the fixture's `.tmp/.../workspace` path and failed to launch Go. Those mistakes and their recovery calls remain part of the operation/time result, rather than being mislabeled as harness rejection.

Model call totals include both actors. Shared `CallStat.Purpose` labels are insufficient to distinguish them in this harness; actor transcripts and the actual `task` execution intervals are used for worker analysis. Source-overlap counts include legitimate search-to-full-read transitions and are not automatically classified as waste.

## Validation and installation

- `go test -p 1 ./internal/agent/...`: passed after the new protection rules and worker handoff were added.
- Focused pruning, latency, source visibility, worker handoff and checkpoint-worker tests: passed after the final instruction-boundary adjustment.
- `go vet -p 1 ./internal/agent/...`: passed.
- Historical replay: expected failure with previous pruning; passed with new pruning.
- `go build -p 1 ./...`: passed on final production sources.
- CLI `--version` and GUI `-h` smoke checks: passed.
- `supercli.exe` and `supercli-web.exe`: rebuilt and atomically installed with checked hashes and portable backups. Existing processes were not terminated. Restart the application to load the new executables.

The Windows checkpoint-worker integration test initially reported no active ref. Diagnostic inspection showed the ref existed; its test-only Git inspection omitted `core.longpaths=true` and silently missed deep paths under the portable temp directory. The helper now matches production long-path handling. No production checkpoint protection was relaxed.

Changed files for this follow-up:

- `internal/agent/agent_tool.go`
- `internal/agent/agent_tool_execute.go`
- `internal/agent/worker_source_context.go`
- `internal/agent/worker_source_context_test.go`
- `internal/agent/context_prune.go`
- `internal/agent/context_prune_active_test.go`
- `internal/agent/context_prune_active_replay_test.go`
- `internal/agent/source_evidence_loop_test.go`
- `internal/agent/checkpoint_worker_integration_test.go`

Portable evidence and scripts: `.tmp/claude-source-efficiency-live-2026-10-12/`, including `combined-summary.json`, individual `result.json`/logs, source manifests, replay logs, build logs and `installed.json`. Other agents' existing modifications were preserved. No commit or release is part of this follow-up.

The three source snapshots and test executables are preserved in CRC-verified `source-snapshots.zip`. Their loose scratch copies were removed after completion (149.9 MB replaced by a 74.0 MB archive); logs, fixture results and executable rollback backups remain. Extract the archive beside the scripts before rerunning an isolated-source comparison.
