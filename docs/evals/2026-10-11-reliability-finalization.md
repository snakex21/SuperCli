# Reliability finalization and conservative cleanup — 2026-10-11

This change groups the reviewed, tested preparation and reliability work since
release 1.0.5, and adds a narrow fix for unnecessary implementation guidance on
status questions. It applies across providers. Selected thinking settings,
generation limits and stored conversation agreements are unchanged.

## Questions and requests

The reported wording `jak poprawił coś agent?` was already excluded by the
existing `jak` guard. Related questions such as `Co poprawił agent?`,
`Czy agent naprawił ten błąd?` and `Did you fix the bug?` still received the
implementation instruction. The classifier now omits this optional guidance for
those status/explanation questions. Polite requests such as
`Czy możesz naprawić ten błąd?` and `Can you fix the bug?` retain it. A later
explicit imperative in the same message also retains it.

This is not routing, permission enforcement or an additional model call. The
canonical user message stays unchanged. The current-turn-only instruction from
the earlier follow-up remains absent from persisted history and later read-only
turns. Tests check both full and thin-tool requests with exactly one provider
call. A compiler overlay of the original `route_map.go` reproduces the new
question regressions; production sources were never reverted for that comparison.

## Disk cleanup

File sizes below are sums of logical file lengths, not a measurement of NTFS
allocation or free-volume space. Deletion targets were resolved and checked to
remain inside the portable application `.tmp` directory; reparse points and an
active compiler cause cleanup to refuse. The removed Go cache had its standard
ownership marker and was distinct from the configured working build cache.

| Operation | Confirmed removed bytes | History impact |
| --- | ---: | --- |
| Old `.tmp/go-cache` | 11,032,974,949 | Rebuildable compiler output only |
| Old Go build directories and abandoned test fixtures under `.tmp/go-temp` | 849,465,034 | No user attachments or test receipts removed |
| Checkpoint unreachable loose objects | 4,690,418 | Zero checkpoint records expired |

The working `.tmp/agent-operation-live/go-cache`, offline module cache, application
databases, settings, evaluation evidence and binary rollback copies are retained.
The one-time inventory changed from 32,431,420,543 to 20,759,047,537 logical bytes
in `.tmp`; the net reduction also accounts for new build/validation/rollback files.
The two empty root notes were documented in
[follow-up notes](../issues/2026-10-11-followup-notes.md), with their originals kept
locally. The legacy `%SystemDrive%` directory was quarantined locally; moving its
957,024 bytes does not reclaim disk space or prove the original producer fixed.

### Why checkpoint storage is still large

Before cleanup, the complete checkpoints + badcheckpoints census measured
9,865,083,248 bytes. It proved a protected floor of 9,200,059,457 bytes, including
latest/index-rooted graphs and four stores with incomplete/unsupported metadata.
The largest store belongs to `Universal_Service_OS`. Unknown stores are retained
as required by the existing fail-closed collector.

The sweep used the existing collector under its native store gate and usage
ledger protocol. A strict-base quota failure with a proved protected floor
permits deletion of graph-proven unreachable loose objects; it prevents history
expiry. The result satisfies the existing managed policy (1 GiB of headroom above
a larger proved floor), rather than claiming a strict 1 GiB whole-store cap.
After cleanup the measured store contains 9,860,392,830 bytes. Removing protected
stores requires explicitly clearing the relevant project's Undo/Redo history.
No broad Git prune/gc or archive deletion by age, name or PID was performed.

## Validation and installed executables

- `go test -p 1 -count=1 ./...`: all 76 tested packages passed.
- `go vet -p 1 ./...`: passed.
- `go build -p 1 ./...`: passed.
- `git diff --check`: passed.
- Question regression: original compiler overlay fails the new status-question
  cases; the final source passes those cases and the full regression suite.
- CLI and GUI were rebuilt and atomically installed with verified rollback
  binaries. CLI `--version` and GUI `--help` smoke checks passed.
- SHA-256 source manifests were checked before/after validation and compilation.
  The previously validated 2,121 main source/test files have only the intended
  `route_map.go` and `route_hint_intent_test.go` changes in this follow-up.
  The broader final manifest also covers 39 pre-existing script/fixture sources.

Tests/builds use `GOMAXPROCS=1` and `-p 1` to avoid competing with VM work.
Completion is awaited without process/log progress polling. Opt-in live-model
benchmarks are not part of this pass, and no new prefill or generation speedup is
claimed. Existing performance measurements remain in the other dated reports.

Local evidence and portable rollback files are under
`.tmp/finalize-reliability-2026-10-11/`. Installed version remains 1.0.5 pending a
future release. Source commits do not publish a release.
