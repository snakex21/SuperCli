# Process completion output retention

This follow-up is separate from explicit tool-list discovery. The existing WebGUI continuation test failed on baseline `06dee973780d7454f2fceae980ec332a166fb8dc`, even with discovery production restored via a Go overlay: 98 of 120 repeated subcases lost the helper's known completion stdout. Both successful and failed exits were affected. This is a local deterministic fixture result, not an estimated live-agent speedup.

The non-PTY process session called `Cmd.Wait` concurrently with its own readers of `StdoutPipe` and `StderrPipe`. Go's [os/exec contract](https://pkg.go.dev/os/exec#Cmd.StdoutPipe) requires those reads to finish before Wait, since Wait closes the pipe. Source inspection used the installed Go 1.26.2 `src/os/exec/exec.go` as well.

The process session now owns its output pipe read ends. Parent writer handles close immediately after successful start; readers drain until EOF independently of `Cmd.Wait`. Completion is published after the readers join. Startup failures close every handle created by this path. Existing child-process scope cleanup, cancellation, lifetime, active/history limits, output retention caps and PTY handling remain in their existing paths.

A descendant can keep inherited writer handles open after the parent exits. The drain therefore has a one-second bound. Expiry closes our reader handles, joins them, and marks output capture incomplete without changing the actual command exit code. The warning survives successful JSON, process lists, compact previews, failed-command diagnostics and pruning. It tells the agent to check a potentially running descendant before relaunching a command. This does not add a new guarantee of descendant termination on platforms where the existing child-process scope cannot provide it.

## Evidence

- Existing WebGUI replay after the fix: 40/40 subcases retain completion output, with both native/thin modes and exit codes 0/7. The baseline used 120 subcases; the denominators differ and are reported explicitly.
- Each replay still uses one discovery, one process start and one completion wait, with five provider requests across two web turns. This fix improves evidence retention; no reduction in those request counts is claimed.
- Focused race tests cover ordinary drain completion, held-pipe timeout, real inherited-handle helpers on Unix, successful/failed exit preservation, warning propagation, normal output capture, stopping a process, cancellation of a wait and context pruning.
- A process that has already exited successfully does not become a command timeout merely because the bounded output drain crosses its lifetime deadline.
- The inherited-handle helper test is Unix-specific because Windows Job Objects can terminate descendants during the existing scope cleanup. Native Windows behavior still requires CI/runtime validation.

Reproduce the integration check:

```sh
go test ./internal/webgui -run '^TestWebProcessSurvivesNewRunWithoutRediscovery$' -count=10 -v
go test -race ./internal/tools/processsession ./internal/agent -run 'TestOutputDrain|TestInheritedOutput|TestProcessExitIsNotRetimed|TestProcessPruningRetainsIncompleteCapture|TestStartCaptures|TestStopCancels|TestCancelledWait|TestWaitReturnsFinal|TestProcessPrune' -count=1 -v
```

The pre-existing background-worker race in `TestBackgroundWorkerFailureReachesCoordinatorBeforeItsReport` was separately reproduced on the untouched baseline. It is not fixed by this change and prevents claiming an entirely clean full race suite.
