# GUI model inactivity during long tool execution

## Observed failure

The completed local GUI session on 2026-10-11 ended at 16:55:21 with
`provider produced no model progress for 5m0s`. Its last native command had an
explicit `timeout_ms: 1800000` (30 minutes), but the persisted tool result recorded
`duration_ms: 300018` and `context canceled`. The GUI's model-progress watchdog
canceled a legitimately running, silent tool after five minutes. This was not a
five-minute limit in the command runner.

## Change

`runStreamWithImages` tracks matching `ToolCallEvent` and `ToolResultEvent` IDs.
The model-progress watchdog is suspended while any foreground tool is active,
including a synchronous delegated worker. It resumes with a fresh interval only
after the last tool returns and no human question is pending. External progress
and question completion cannot rearm the timer while a tool is still running.

Caller cancellation, process-tree cleanup, positive tool deadlines and the
provider's own transport timeout remain enforced. No model, selected reasoning
level, user configuration or stored conversation is changed. The rule uses tool
lifecycle events, with no model, website or executable names.

The shared tool descriptions also clarify that omitted/zero `timeout_ms` has no
command runtime cap, and that persistent servers/watchers should use
`process_session` and reuse its ID across calls and turns in the same workspace.
The existing per-workspace process manager already provides that lifetime.

## Validation

- A Go compiler overlay using the pre-fix `stream_run.go` reproduces the GUI
  cancellation on an actual native curl command connected to a local HTTP
  fixture. The configured model-idle interval is shortened to 200 ms so the
  failure can be reproduced without a five-minute wait. Production source is
  not overwritten by the baseline experiment.
- The fixed native-command regression passes all four scenarios: silence longer
  than the model-idle limit followed by successful completion; a genuinely
  stalled model after the command; explicit Stop and socket/process cleanup;
  and a positive command deadline longer than the model-idle interval.
- The tests verify delivery of the retained command result, no repeated command,
  a durable conversation-completion receipt and no active-work residue.
- Full tests and `go vet -p 1` passed for `internal/tools/ctxexec`,
  `internal/tools/workflow`, `internal/tools/processsession`, `internal/agent`,
  `internal/ui/tui` and `internal/webgui`. Existing GUI process-continuation tests
  exercise reusing the same process across separate conversation turns.
- The earlier 48 optimized source files retain their recorded SHA-256 hashes.

Receipts and logs are kept locally under `.tmp/remove-ofm-2026-10-11/`.
No GPU benchmark or inference service was started for this regression.
