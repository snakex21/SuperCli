# Portable startup audit — 2026-09-27

The completion audit found two startup paths that could write outside the chosen
portable data directory. Neither requires a model or changes provider requests.

## Legacy migration

The previous migration copied legacy ~/.supercli data, then created or overwrote
MOVED.txt in that legacy directory. Migration now leaves the source unchanged;
only the destination receives copied data and rewritten project references.
Existing portable installations also return before looking up the old profile.

Tests cover both absent and preexisting MOVED.txt, complete source file equality,
copied sessions/configuration, destination-only reference relocation, repeated
startup and an already populated destination. The old implementation failed the
two source-preservation cases; the updated implementation passes.

## Browser fallback

When native WebView2 failed, GUI startup previously cleared profileDir after a
MkdirAll error and launched Chromium without --user-data-dir. If no app-mode
browser could launch, it opened the default browser automatically.

OpenAppWindow now rejects empty or unavailable profiles before starting any
browser, resolves the chosen profile to an absolute path, and creates/reuses it.
Run closes its HTTP server and returns an actionable error if portable window
startup fails. The user can select a writable --data-dir or explicitly launch
--no-window and choose how to open the URL. There is no automatic default-profile
fallback. Successful native/app-mode paths remain available.

Tests verify blank/blocked paths start no browser, preserve existing files, and
that a newly created or reused local directory is passed via --user-data-dir.
This is a portability correction, not a measured model-latency optimization.

## Validation and installed artifacts

- go test -timeout=90s ./...: pass.
- go vet ./...: pass.
- CLI and Windows GUI builds: pass; CLI --help: pass.
- GUI echo-mode smoke: health HTTP 200, embedded HTML HTTP 200 (12968 bytes).
  One request to each endpoint, no retry loop; isolated data/workspace under
  .tmp. Only the smoke process was stopped. This is not a visual WebView2 test.
- Both executables replaced in the application directory; previous executables
  retained under the artifact directory and installed SHA-256 verified.
- OpenCode Zen transport files have no diff against HEAD. Existing worktree
  changes were preserved; no commit or push was made.

Artifacts: .tmp/portable-migration-readonly-2026-09-27/. The first smoke harness
incorrectly waited for stdout; this Windows GUI writes its startup message into
its local log. A filesystem-notification attempt also timed out despite a saved
startup message. The final single-request HTTP smoke passed. These two failed
readiness detectors are retained as evidence and were not production failures.

Installed CLI SHA-256:
34988961468f61140137a54b49dcf2e364b5a27147113e24ddaa5545170d43ae

Installed GUI SHA-256:
e9a4a2e6040eedfdd3673c67ff6759326df4c07be346e4da3789b7246bc7b02c

## Optimization cycle completion

The confirmed issues selected from saved sessions, transport fixtures and the
completion audit are implemented, tested and included in the installed builds.
The fresh production-profile workflow succeeded on both local Qwen and free Zen:
one worker handled the task and the follow-up, supplied tests remained unchanged,
and passing results were present before later verification. See
[the live report](2026-09-27-fresh-production-workflow.md).

This closes the current evidence-driven optimization cycle. It does not assert
that every future task is optimal or that all additional model checks are waste.
There is no clean aggregate before/after latency percentage for the whole cycle.
Potential shell-wrapper classification gaps and model-selected extra verification
remain hypotheses for a future representative failure, not demonstrated unfinished
fixes. Future changes should follow new real-session evidence rather than adding
standing instructions or speculative restrictions.
