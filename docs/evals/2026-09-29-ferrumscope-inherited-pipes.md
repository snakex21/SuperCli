# FerrumScope: completed launcher reported as a failed command

## Saved evidence

Session ce6986b1affaf49d contains two ctx_execute results with `exec: WaitDelay expired before I/O complete`, at message sequences 2206 and 2289. At 2289, the launcher printed a background installer PID but SuperCli returned `command_failed exit=-1`. The next saved check, sequence 2291, found that PID running; sequence 2294 found the installation files. The audit did not launch an installer or rerun the saved commands.

## Cause

Go returns exec.ErrWaitDelay when the parent process exits successfully but inherited output pipes stay open past WaitDelay. Runner classified this as a generic execution failure. That confuses a completed launcher with failed work and may encourage an unnecessary second launch. The log does not demonstrate a duplicate installation; this is a correctness fix, not a measured count of prevented retries.

## Change

For exec.ErrWaitDelay without cancellation/timeout, retain the actual parent exit code 0 and return explicit output_incomplete metadata plus a warning that a descendant may still be running. Keep the bounded pipe wait. This does not establish that the background task completed successfully. Nonzero process exits, timeouts and cancellation keep their failure behavior.

The warning survives the bounded model preview and the full UI result. History pruning retains output_incomplete=true next to the exit code, so a shortened old result does not silently imply complete capture. Both fields are omitted for ordinary commands; there is no added system instruction, model request or disk capture.

## Verification

The regression test failed before the change with exit=-1 despite a successful launcher. After the change, real child-process tests verify exit=0 with incomplete-capture metadata and preserve a genuine exit=7 with inherited pipes. Workflow coverage verifies the raw result, model-visible stored-output preview and its existing size budget. Agent tests verify preservation through pruning and provider messages with both full and thin tool exposure; log text cannot forge the metadata.

Focused ctxexec, workflow and agent tests pass. Full go test -timeout=90s ./... and go vet ./... pass. Both TUI and Windows GUI builds and their --help smoke checks succeed. Detailed results are retained locally in .tmp/ferrumscope-inherited-pipes-2026-09-29.
