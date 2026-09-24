# Persist tool discovery across turns — 2026-09-23

A fresh GUI loop previously rebuilt an empty activation set. After a successful
tool_search in one turn, a later invoke_tool could fail with "not active; call
tool_search once" even though the tool was still registered and already known.
A regression reproduced that refusal before this change.

Explicit tool_search discoveries now have a small snapshot in the portable
session database. The shared agent loop restores it before preparing a turn and
saves changes when the turn exits, including controlled cancellation. TUI /resume
also imports the source session's snapshot when loading its conversation into a
different running session.

Discovery is separate from automatic tool promotion. Merely exposing Word tools
for a document turn or marking a tool always available does not create a saved
discovery. Explicit tool_search results still activate tools through the normal
registry; restored discovery cannot register a removed/restricted tool or bypass
argument validation, execution verification or tool-specific approval.

The snapshot is read once per new loop and written only when its discovered set
changes. It stores names, not copied schemas, prompts or credentials. Old sessions
without a snapshot start empty; subsequent discoveries are saved. Rewinding
messages invalidates the snapshot, and deleting a session removes it via the
existing foreign-key lifecycle.

No new model instruction, tool description or helper inference was added.
Native-tool mode exposes the restored schemas as it would in a continuing live
loop; thin/stable mode keeps its existing dispatcher behavior. This does not
guarantee a model will never choose to repeat discovery, and it does not add
durable storage for large read_output handles. Special OpenCode Zen transport
behavior is untouched.

Validation:
- Before: the fresh-loop regression executed the target zero times and reported
  the spurious "not active" refusal.
- After: continuation executes the tool exactly once using two model calls
  (tool request, final answer), with no repeated tool_search.
- Integration coverage runs the actual GUI loop construction twice against one
  session store; a separate test covers TUI /resume.
- Tests cover controlled cancellation, one metadata read per loop, no writes for
  unchanged state, automatic promotion exclusion, unavailable tools, visibility
  reset, database reopening, session isolation, corrupted metadata, rewind and
  session deletion.
- Full go test ./...: all 64 tested packages passed.
- go vet ./... and git diff --check passed.

No live-model timing claim is made for this change. It removes a demonstrated
technical cause of extra model/tool round trips. Evidence:
.tmp/tool-discovery-before.json, .tmp/tool-discovery-focused.json and
.tmp/tool-discovery-checks.json.

Both executables were rebuilt, passed --help smoke checks, and installed with
byte-for-byte verification. Previous versions: C:\Users\ASRock\Desktop\SuperCli\SuperCli\.tmp\previous-binaries-paXTKa.
Install record: .tmp/tool-discovery-install.json.
