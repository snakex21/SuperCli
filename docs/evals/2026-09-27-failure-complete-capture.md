# Keep early diagnostics from complete command captures — 2026-09-27

## Reproduced problem

FailureSummary used head/tail evidence only when Runner created a separate retained capture. A stream below the requested stdout/stderr preview limit had no such object, so its failure summary kept only the last 2 KiB even though the complete stream was available. Increasing a command's output limit could therefore hide the first diagnostic.

The fixed GunMayhem snapshot showed this shape at sequences 1023 and 1546. Two exact output handles were retrieved read-only from the portable sessions.db and frozen in retained-snapshot.json. The saved gofmt output starts with the net_backend.go diff header, but its inline failure began midway through an unrelated hunk. The saved firewall output likewise lost its initial section header. Neither replay executes the original command or modifies the user project.

## Change

When there is no separate retained object, an untruncated stream now uses the existing head/tail formatter. Already-truncated/partial snapshots retain their tail-only interpretation and label. This shared formatter also serves completed process_session failures. Exit/timeout/error status, raw UI JSON and retrievable output evidence remain intact.

No extra process, file read, provider call, schema or prompt instruction is added. The per-stream 2 KiB budget is unchanged, and short errors retain the same representation. Special OpenCode Zen behavior and portable data storage are unaffected.

## Exact replay

| Saved result | Complete stdout | Old summary | New summary | Initial header visible |
| --- | ---: | ---: | ---: | --- |
| gofmt diff, seq 1546 | 19,457 bytes | 2,103 bytes | 2,051 bytes | No → Yes |
| firewall query, seq 1023 | 2,271 bytes | 2,103 bytes | 1,991 bytes | No → Yes |

The summaries retain their final evidence too. Middle evidence remains available through read_output. These measurements establish better inline evidence within the existing budget; they do not establish a saved live-model turn or a wall-clock speedup.

## Verification

- Real child-process failures at 1, 4, 16 and 64 KiB requested preview limits preserve the early and final markers from both streams. Before the change, the 16/64 KiB cases lost both early markers.
- Exact saved-output replays failed before and pass after the change.
- Native and thin loop tests send the diagnostic to the next provider request with one command execution and two scripted provider calls, without a read_output call or rerun. UI keeps the original full output.
- Existing retrieval tests still recover middle diagnostics through the output handle. UTF-8, length caps, timeout and missing-program behavior pass.
- process_session tests distinguish complete and already-partial captures, keeping accurate truncation labels.
- Full go test ./... with the saved replay enabled (90-second package timeout), go vet ./..., CLI/GUI builds, CLI --help smoke and git diff --check pass.

Artifacts, original source backup and frozen output bodies: .tmp/failure-complete-capture-2026-09-27/.

## Installation

Both portable executables were installed and their SHA-256 hashes verified; installed.json records backups and hashes. No user process was terminated. Restart CLI/GUI to load the change. The broader efficiency goal remains active.
