# Successful Go test retry with different verbosity — 2026-09-27

## Confirmed defect

Saved coding traces contain failed go test ./... followed by successful go test ./... -v, and the reverse sequence. The failed-check tracker compared exact argv identities, so changing only output verbosity left the original failure pending. The goal tool then rejected complete_task despite an actual successful rerun of the repaired code.

This is a separate defect from the model repeating calls on its own. The earlier Muse worker had no goal call, so this guard did not cause that specific repetition.

Six real-Go regression scenarios reproduced the false block: native and text tool protocols in both verbosity directions, plus foreground failure/background success and background failure/foreground success. Each fixture fails a fixed test, patches production code without modifying that test, reruns it successfully, then attempts goal completion.

## Change

A shared VerificationCommandKey normalizes output verbosity for a plain go test package list. It recognizes -v and -test.v, including explicit true/false forms. The agent's foreground bookkeeping and the managed-process identity use the same function.

The executable, package list/order, workdir and effective explicit environment remain part of the identity. Unknown flags, shell wrappers, forwarded arguments and options such as -run, -short, -race, -list, -count and -vet keep exact comparison. The normalizer deliberately does not parse arbitrary Go/custom flag combinations; for example, a command containing -run is kept exact even if its separate -v flag changes.

CommandKey remains an exact execution identity, separate from verification matching. No command is skipped, cached or synthesized by this change. Success still requires the actual tool result. Internal identity metadata adds no prompt/output fields. No provider routing, permanent instructions or storage locations changed.

## Verification

- Six real-process red/green cases finish after one failed and one passing test; goal completion was rejected before and accepted after.
- Native/text protocol coverage includes both adding and removing verbosity.
- Managed-process tests await completion directly, without polling.
- Tests retain distinctions for narrower package/test scope, other executables, workdirs, environments, unknown/custom flags and failure status.
- Existing environment-order, encoded-argument, process identity and metadata privacy tests pass.
- An initial text-protocol test fixture incorrectly quoted scalar values; it was corrected to the documented call syntax before confirming that path's red/green result.
- Full go test -timeout=90s ./..., go vet ./..., CLI/GUI builds, CLI --help and git diff --check pass.
- Both executable copies installed with backups and SHA-256 verification.

This removes a reproducible false demand for another verification when only plain Go-test verbosity changes. It does not prove a whole-session latency reduction or that models stop repeating checks on their own. Go's installed help testflag describes -v as verbose test/log output; other execution/scope flags remain distinct.

Artifacts: .tmp/worker-completion-format-2026-09-27/ contains verbose-red.json, the corrected verbose-thin-red.json, verbose-green.json, full validation and installation records, plus the separate format-comparison experiment.
