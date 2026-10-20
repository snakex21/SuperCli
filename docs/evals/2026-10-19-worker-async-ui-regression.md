# Restore the existing live delegation view

The follow-up screenshot reproduced a live background-task defect beyond the
previous transcript journal fix. A real Engine/AgentTool regression test with a
scripted provider and three actual filesystem reads failed before correction:
all eight worker progress events had an empty originating task-call ID, and the
production browser scripts rendered duplicate delegation cards.

## Causes and corrections

1. The GUI parser rejected the host's asynchronous acknowledgement because its
   field allowlist lacked `continuation`. It displayed the model-facing XML as
   ordinary tool output instead of binding the running card to its worker ID.
   The parser now accepts that field as protocol metadata without displaying
   the instruction. Pending-state text uses the existing UI translation.
2. Detaching a worker intentionally drops the parent Run channel, but also
   dropped its task-call ID. After the acknowledgement consumed the open-tool
   entry, delayed progress could no longer find its card. Background context
   now retains the ID and verification observer while still dropping the Run
   channel. The lifetime progress sink includes the original task-call ID.
3. Completion events carried only the report and aggregated tool names. The
   existing bounded UI journal now accompanies that event, so missing progress
   can be restored by the application. Complete live activity keeps its DOM;
   repairing partial activity preserves expanded results. Known invocation/ID
   mismatches cannot update a newer worker's card.
4. Current stored results may include the host's timestamped historical tool
   observations suffix. The GUI previously recognized only its older suffix,
   rejecting an otherwise valid result. The parser accepts the current bounded
   producer header with the matching worker ID as well as the legacy format.
5. History now folds an asynchronous acknowledgement and its completed report
   into one card. The display journal retains the original task-call ID for
   exact pairing across history pages. Continuations remain separate cards.

No prompt, model instruction, reasoning selection or model-facing result is
changed. Activity comes from actual host tool events, with no additional model
call, extra per-tool database write, or polling introduced by this correction.

## Regression verification

`TestWorkerAsyncUIActualRunAndHistory` drives the real Engine, background task
tool, read-only worker, three filesystem tools, event-to-wire conversion and
canonical SQL transcript. It then renders those actual wire events and DTOs
using the production GUI scripts in a native headless browser. The scripted
provider replaces external inference only; it never generates an activity log.

The test checks actual delivery order, the acknowledgement arriving before all
progress, another parallel task with the same role, partially missing progress,
entirely missing progress, and one-card history restoration including an older
acknowledgement arriving from a preceding page. Tool results remain expandable,
the final report remains visible, XML/control instructions stay out of the
display, and an already expanded result survives activity repair.

The original failing regression passes after the corrections. It additionally
checks that an explicit foreign parent-call binding cannot be merged by worker
ID alone, and that an older versioned completion cannot overwrite a newer
invocation even if its parent-call ID was reused. A live acknowledgement shows
the waiting placeholder until its first tool event.

Final verification passed:

- real asynchronous Engine-to-browser regression;
- worker regressions in `internal/agent` and `internal/webgui`;
- `go test -race` for worker UI, real async execution and progress tests;
- `go test ./... -count=1 -timeout=10m` (373.77 s);
- `go run ./scripts/test-ui -timeout 7m`: 558 passed, no skips or failures;
- `go vet ./...`, `go build ./...` and both executable builds;
- CLI version and hidden GUI help smoke;
- scoped `git diff --check`.

After two final UI-only guard/placeholder refinements, the relevant native
browser tests and all 558 UI checks passed again, and both binaries were rebuilt.
The fixture screenshot was inspected. Test setup initially needed the actual
shared runtime included before loading the GUI helpers; failure records remain
in the stage logs alongside the passing runs.

The standard portable `supercli.exe` and `supercli-web.exe` were updated, as were
the two alternate executable names linked by the preceding fix. Copies were
verified against staged builds with SHA-256. Previous binaries are preserved in
`.tmp/worker-ui-async-2026-10-19/previous-builds/`. Test logs and the browser
fixture screenshot are in that stage's parent directory. Existing application
data was not changed. No commit or release was made.
