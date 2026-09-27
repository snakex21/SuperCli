# Verification command identity — 2026-09-26

## Reproduced failures

Two deterministic regressions were reproduced in the current code:

1. A failed direct check with environment overrides FIRST=1, SECOND=2 remained unresolved after a passing rerun with the same overrides in reverse order. The goal-completion guard could therefore request another check even though an equivalent check had passed.
2. process_session uses env, while failure tracking only parsed env_extra. Terminal snapshots carried no environment identity. A passing process with TEST_MODE=quick could erase the failure from TEST_MODE=full.

The captured GunMayhem slice contains no process_session calls, so the second finding is code/test evidence, not an attribution for that session's wall time.

## Change

The existing failed-check tracker now uses a shared internal command key comprising argv, normalized working directory and canonical explicit environment overrides. Ordering is ignored, the last value for a repeated key wins, and key case is folded only on Windows.

A managed process computes the key once at start and carries it through completion/wait in internal Result metadata. No environment values or digest are appended to UI JSON, saved tool output or model context. An unattributed legacy snapshot cannot resolve a check with a different/unknown environment. Stop/list/resize and still-running processes remain management events, not passing verification.

Equivalent foreground/background retries can resolve each other. Different arguments, directories or explicit override values remain distinct. This extends the existing command-evidence guard; it does not add automatic test reruns or a new model call.

Scope remains explicit overrides. It does not prove arbitrary shell-script equivalence or that an external service/machine state is unchanged. No permanent prompt/schema changes, provider routing changes, Zen changes or app-data relocation were made.

## Validation

- Both behavioral tests were red before implementation and green afterward.
- Tests cover equivalent reordered/duplicated overrides, differing values, Windows/POSIX case rules and preserving caller arguments.
- A real helper subprocess runs through process_session start and blocking wait; its internal key survives completion while output remains free of environment metadata.
- Foreground/background equivalence and unknown-environment handling pass.
- Full go test ./... and go vet ./... pass.
- Private command logs are under .tmp/check-identity-2026-09-26/. No live provider benchmark was used.
- Windows CLI and GUI rebuilt and installed with backups under the artifact directory; CLI --help smoke check and installed-file hashes passed. Running instances require restart.
