# Verification recovery across accepted argument forms — 2026-09-26

## Confirmed fault

The command executor already accepts an argv list supplied as a JSON-encoded string. Its registry uses the registered schema to convert that form to a string array before execution. The same applies to explicit environment additions.

Failed-check bookkeeping instead decoded the original model arguments directly into typed arrays. A change in representation was enough to lose an outcome:

1. A normal array-form test fails and becomes pending.
2. The same command/environment is retried with an encoded argv or environment array.
3. The registry executes it successfully.
4. Bookkeeping silently returns on a JSON type error.
5. The existing goal completion guard rejects completion, saying the check has no successful rerun.

The reverse direction also lost evidence: a failing check emitted in the accepted encoded form was not retained. This is a mismatch between execution and bookkeeping, not a reason to add another model instruction or ask the model to rerun a passing test.

## Change

Registry.CoerceArgs exposes the existing cached-schema coercion without executing the tool. recordCheckResult uses it only when ordinary typed JSON decoding fails. Plain arguments stay on the existing fast path. Process action/identity/environment fields are decoded together instead of decoding the operation envelope a second time.

The fix handles accepted representations for both ctx_execute and process_session. Command/workdir/environment identity rules remain in place: passing a different environment does not resolve the original check. Shell command strings such as "go test ./..." remain invalid where an argv list is required.

No tool schema, provider route, prompt, background model call or persisted application data was added.

## Evidence and validation

The new regression failed before the production change in six encoded argument combinations, plus the native-call coordinator replay:
- normal failure followed by encoded success remained pending;
- encoded failure was not retained;
- the coordinator executed the successful retry but the goal action was rejected.

After the change:
- All eight normal/encoded command/environment direction combinations pass.
- Malformed command/environment forms cannot clear a prior failed check.
- A different passing environment cannot hide the original failure.
- Custom process start results without an internal command key use the correctly decoded environment; equivalent retries resolve, different environments do not.
- Native and text-protocol coordinator replays complete with two command executions: the original failed check and its successful retry. The existing text parser already decoded the particular sentinel fixture before this change; it is retained as a compatibility check.
- An integration test uses the real registry and command runner on a temporary Go module. It runs a failing test, edits the fixture, runs the identical command/environment using encoded arrays, and completes the goal without another execution.
- Full go test -timeout=90s ./... and go vet ./... pass.

The focused before/after tests establish elimination of a false completion rejection. They do not establish how frequently models change argument representation in ordinary sessions or quantify an average provider-latency reduction. The integration test does not contact a model or network service.

Code: internal/agent/failed_check.go, internal/tools/core/registry.go, internal/agent/failed_check_args_test.go.

Local artifacts: .tmp/check-args-2026-09-26/before.json, after.json, real-and-process-tests.json, suite.json and vet.json.

## Shell-wrapper investigation

The fixed session snapshot contains 259 command calls, of which 86 use a shell. Seven contain a Go test invocation inside PowerShell: two successful, four failed and one blocked before execution. They include environment setup and, in several cases, Resolve-Path.

The current direct-command classifier does not recognize those scripts as verification commands. No goal-completion rejection attributable to that limitation was found in this snapshot, so the shell hypothesis was not presented as a proven cause of redundant turns and no speculative shell parser was added. Compound scripts and their exit status still need separate investigation.

The confirmed argument-form defect was reproduced from the common code path and deterministic tests; it was not claimed to have occurred in that historical snapshot. Shell audit details stay locally in shell-check-audit.json.

## Installed build

Both Windows executables were rebuilt and installed after the passing checks; CLI --help passed. Backups and verified installation hashes are under .tmp/check-args-2026-09-26/before/ and installed.json. Restart running instances for the new build.
