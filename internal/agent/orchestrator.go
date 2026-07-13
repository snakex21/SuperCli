package agent

import "supercli/internal/tools"

// Orchestrator mode is the HARD delegation switch. Unlike coordinator
// mode (a prompt-only nudge that still leaves every tool reachable via
// tool_search), orchestrator mode PHYSICALLY restricts the main loop's
// registry: the coordinator holds only delegation + a read-only lookup
// set, so it cannot edit files, run commands, or do heavy work itself —
// it must hand that to a `task` worker. The guarantee is enforced by the
// registry (Registry.Execute on a mutating tool simply returns "unknown
// tool"), not by asking the model nicely.
//
// The switch is a NEW-SESSION setting: swapping the tool list mid-session
// would break the KV-cache prefix (chat templates serialize `tools` at
// the very start of the prompt). `/orchestrator on|off` persists to
// config.toml and takes effect on the next launch.

// orchestratorTools is the exact set the main loop may hold in
// orchestrator mode: the delegation trio plus a read-only lookup set.
// Every mutating/executing tool (edit_line, write_file, ctx_execute,
// darwin, move/copy/trash, edit_docx, ...) is deliberately absent — the
// coordinator delegates all of that to workers. remember/goal write only
// coordinator bookkeeping (memory DB, goal state), never the filesystem,
// so they stay to keep the memory/planning contract working; ask_user is
// pure interaction. Anything doubtful is left out (worker territory).
var orchestratorTools = []string{
	"task", "send_message", "task_stop",
	"tool_search", "read_lines", "read_context", "list_dir", "recall",
	"apply_skill", "ask_user", "goal", "remember", "scratchpad",
}

// orchestratorCoreTools is the schema-carrying core for orchestrator mode
// under the thin tool protocol: these carry a full JSON Schema every turn,
// the rest of orchestratorTools are advertised in the compact catalog and
// pulled in via tool_search. task MUST be here — delegation is the main
// loop's primary action, so (list_dir lesson) it has to be directly
// callable with a full schema from turn 1, never buried in the tail.
var orchestratorCoreTools = []string{
	"task", "tool_search", "read_lines", "read_context", "read_output", "list_dir", "recall",
}

// isOrchestratorCore reports whether name is in the orchestrator thin-core.
func isOrchestratorCore(name string) bool {
	for _, n := range orchestratorCoreTools {
		if n == name {
			return true
		}
	}
	return false
}

// OrchestratorRegistry returns a fresh registry containing ONLY the
// orchestrator tool set, copied from base. Every tool is marked always-on
// (visible each turn); the thin partition then decides which carry a full
// schema vs. sit in the catalog. Tools missing from base are skipped
// silently. The result is a brand-new registry so the main loop and the
// worker BaseRegistry (which keeps the full set) have independent state —
// a mutating tool the workers still own is simply not present here.
func OrchestratorRegistry(base *tools.Registry) *tools.Registry {
	out := tools.NewRegistry()
	for _, name := range orchestratorTools {
		t, ok := base.Get(name)
		if !ok {
			continue
		}
		if err := out.Register(t); err != nil {
			continue
		}
		out.MarkAlwaysOn(t.Name)
	}
	return out
}

// OrchestratorPrompt returns the extra system section for orchestrator
// mode. Kept lean (tokens matter on the coordinator prefix): it states the
// hard boundary (no edit/run tools) and the one move that follows from it
// (delegate via task). It replaces CoordinatorPrompt when active.
func OrchestratorPrompt() string {
	return `

## Orchestrator mode

You are the orchestrator. You have NO tools to edit files, run commands, or
change anything — only read tools (read_lines, read_context, list_dir, recall)
and delegation. For ANY change, code execution, test run, or deeper
investigation, delegate to a worker with the task tool. Write each task prompt
self-contained: goal, relevant file paths/lines, expected result, and what
"done" means — the worker does not see this conversation.

Use your read tools only for quick lookups to plan the delegation. Answer
simple conversational or explanatory questions directly, without tools. After a
worker reports back, summarize the outcome for the user and keep only the
decisions, changed files, and verification status in the main chat. Use
scratchpad for detailed worker evidence that should survive without bloating
the conversation.`
}
