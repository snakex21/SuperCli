# Foreground command timeout and cancellation — 2026-09-26

## Confirmed problems

The September 25 GunMayhem snapshot contains command timeouts at messages 633, 842 and 1273, including `go test` runs. The command schema and runner both imposed a hard 30-second ceiling. A code worker could not request enough foreground time for an ordinary longer build/test; its restricted tool set does not include managed background processes.

Separately, `ctx_execute` converted a caller's cancellation/deadline into a new plain error. This discarded `errors.Is` identity, so the agent loop's existing cancellation handling did not recognize the interrupted command and could count it as an ordinary failure.

## Change

- Explicit `timeout_ms` can now request up to 300,000 ms (five minutes). The schema derives its maximum from the runner constant, keeping validation and execution aligned. Default remains 10,000 ms; shorter explicit limits and caller cancellation still apply. Commands return immediately when they finish.
- The existing tool-description sentence was replaced with guidance to choose an explicit timeout for long builds/tests; there is no additional worker tool or permanent prompt block.
- Interrupted command errors preserve the caller's cause through the self-contained error wrapper. A command exhausting its own timeout remains an ordinary command failure. Successful completion is not reclassified if cancellation races with it.
- The loop can now use its existing interrupted-outcome handling without counting the stop against the repeated-failure gate or marking it as failed verification evidence.

No automatic retry, background conversion, polling, provider call or provider-specific path was added. This applies to CLI, GUI, the parent agent and code workers on local/cloud backends. The special Zen transport is unchanged.

## Reproduction and validation

An opt-in test launches a real child process that prints STARTED, takes 31 seconds and prints FINISHED, with an explicit 60-second timeout:

| Version | Result |
| --- | --- |
| Before | Killed at 30.0 s; exit 124; FINISHED absent |
| After | Completed at 31.02 s; FINISHED present |

This proves removal of the premature ceiling, not a general end-to-end speedup. Historical timeouts may also reflect actual slow/failing tests; increasing the available timeout does not establish their correctness.

Fast regressions also verify schema acceptance of 60/120/300 seconds, rejection above five minutes, caller cancel/deadline identity, and the distinction from the command's own 100 ms timeout. An integration test runs the real tool through the agent loop and checks that interruption does not pollute the retry/verification guards.

The 31-second test is opt-in (`SUPERCLI_TEST_LONG_COMMAND=1`) so routine suites remain fast. It was executed before and after the change; no model or network was involved.

Full `go test -timeout 90s ./...`, `go vet ./...`, `git diff --check`, and both executable builds passed. Raw results: `.tmp/long-command-2026-09-26/`.

Both executables were installed after a CLI help smoke check. Backups and verified
SHA-256 installation records are retained in the local result directory. Restart
running instances to load the new build.
