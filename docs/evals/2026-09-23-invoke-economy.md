# Avoidable tool-envelope repair turns — 2026-09-23

## Evidence and scope

Inspection of recent saved session telemetry showed that model waiting and
generation dominate runtime; request preparation generally took a few milliseconds.
The history also contained invoke_tool refusals for built-in remember and task
calls, including calls with valid target arguments. The refusal asked the model
to generate the same call again directly.

The dispatcher had a separate hand-maintained core allowlist that omitted these
tools. It also dropped target arguments written beside the tool name rather than
inside args or arg.<field>. These are envelope-format issues, not failed actions
that should be hidden or ignored.

## Change

The dispatch allowlist derives from the existing built-in full-schema core sets
plus explicitly supported memory/document helpers. Built-ins still have to be
visible in the current registry; arbitrary always-on extensions and dormant
mutations retain their activation gate. A restricted worker/orchestrator registry
remains restricted.

Native args objects, arg.<field> fields and top-level target fields now share the
same decoder. Values are preserved and duplicate fields are rejected as ambiguous.
The target's normal schema validation, cancellation checks, execution, verification
and result reporting still apply. Incorrect arguments remain errors.

Wrapped tool_search can activate a tool at its normal execution barrier, including
a search followed by invocation in the same model response. Recursive invoke_tool
wrapping remains rejected. Wrapped task batches reach the usual concurrency
policy: sequential when configured, parallel when configured.

No tool description, schema, system instruction, provider request dialect or
OpenCode Zen transport was changed. No additional model request was introduced.
GUI, TUI and workers share the corrected agent loop.

## Controlled before/after replay

The deterministic test provider models a repair observed in the logs: first emit
an envelope; if it is refused, resend identical target arguments as a direct call;
after success, return the final answer. Tools execute only in isolated fixtures.

Twelve variants: remember/task × native args/arg fields/top-level fields × normal
and thin tool modes. The original code required 3 provider calls in every variant
and failed the new two-call regression. The corrected code requires 2 calls in
every variant, with exactly one target execution and one verification.

| Scenario | Before | After |
| --- | ---: | ---: |
| Model requests per variant | 3 | 2 |
| Target executions | 1 | 1 |
| Target verifications | 1 | 1 |
| Added model instructions | 0 | 0 |

This removes one repair round-trip when the affected envelope occurs. It is a
controlled replay, not a live-provider latency or billing measurement; it does
not imply a universal 33% speedup. Proper direct calls already worked and retain
their path. Serialized request-byte counts are also retained in the raw replay
logs, but they are not provider-billed token counts.

## Validation

- go test ./... and go vet ./... pass.
- All twelve replay variants preserve the target call/result ID, name and values.
- Hidden built-ins, inactive mutations, restricted-registry targets and recursive
  dispatch stay blocked.
- Required arguments remain validated and canceled calls do not execute.
- Ambiguous duplicate envelope fields are rejected.
- Wrapped discovery and target execution can share a model response, preserving
  normal ordering and verification.
- Wrapped worker batches preserve configured concurrency and ordered results.
- Existing tool-dispatch, goal, activation and verification tests pass.

Replay tests: internal/agent/invoke_economy_test.go.
Raw local reports: .tmp/invoke-economy/before-replay.json,
after-replay.json and full-checks.json.
