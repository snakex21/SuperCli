package agent

import (
	"context"
	"encoding/json"
	"testing"

	"supercli/internal/tools"
)

// orchestratorBaseRegistry builds a registry that mixes the orchestrator
// tool set with several mutating/executing tools the main loop must NOT
// keep in orchestrator mode.
func orchestratorBaseRegistry() *tools.Registry {
	r := tools.NewRegistry()
	noop := func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
		return tools.Result{Text: "x"}, nil
	}
	all := []string{
		// orchestrator set
		"task", "send_message", "task_stop",
		"tool_search", "read_lines", "read_context", "list_dir", "recall",
		"apply_skill", "ask_user", "goal", "remember", "scratchpad",
		// mutating / executing tools that must be stripped
		"edit_line", "edit_lines", "insert_after", "delete_lines",
		"write_file", "make_dir", "move", "copy", "trash",
		"ctx_execute", "edit_docx", "edit_xlsx", "darwin",
	}
	for _, n := range all {
		r.MustRegister(tools.Tool{Name: n, Description: "d " + n, Schema: `{"type":"object"}`, Fn: noop})
		r.MarkAlwaysOn(n)
	}
	return r
}

// TestOrchestratorRegistry_OnlyAllowedTools: the restricted registry
// contains exactly the orchestrator tool set — every mutating/executing
// tool is physically absent (Execute would return "unknown tool").
func TestOrchestratorRegistry_OnlyAllowedTools(t *testing.T) {
	base := orchestratorBaseRegistry()
	out := OrchestratorRegistry(base)

	got := map[string]bool{}
	for _, n := range out.Names() {
		got[n] = true
	}
	// Every orchestrator tool present.
	for _, n := range orchestratorTools {
		if !got[n] {
			t.Errorf("orchestrator tool %q missing from restricted registry", n)
		}
	}
	if out.Len() != len(orchestratorTools) {
		t.Errorf("restricted registry has %d tools, want %d (%v)", out.Len(), len(orchestratorTools), out.Names())
	}
	// No mutating tool leaked in.
	for _, banned := range []string{
		"edit_line", "edit_lines", "insert_after", "delete_lines",
		"write_file", "make_dir", "move", "copy", "trash",
		"ctx_execute", "edit_docx", "edit_xlsx", "darwin",
	} {
		if got[banned] {
			t.Errorf("mutating tool %q leaked into orchestrator registry", banned)
		}
		if _, ok := out.Get(banned); ok {
			t.Errorf("orchestrator registry can still Get(%q) — not physically stripped", banned)
		}
	}
}

// TestOrchestratorRegistry_SkipsMissing: tools absent from base are
// skipped silently rather than panicking.
func TestOrchestratorRegistry_SkipsMissing(t *testing.T) {
	r := tools.NewRegistry()
	noop := func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
		return tools.Result{Text: "x"}, nil
	}
	r.MustRegister(tools.Tool{Name: "task", Description: "d", Schema: `{"type":"object"}`, Fn: noop})
	r.MustRegister(tools.Tool{Name: "recall", Description: "d", Schema: `{"type":"object"}`, Fn: noop})
	out := OrchestratorRegistry(r)
	if out.Len() != 2 {
		t.Fatalf("want 2 tools (task, recall), got %d: %v", out.Len(), out.Names())
	}
}

// TestOrchestrator_ThinCorePartition: in orchestrator + thin mode, `task`
// carries a full schema (schema-core) and the non-core orchestrator tools
// (send_message, ask_user, ...) sit in the advertised tail. Mirrors the
// list_dir lesson: the turn-1 primary action must not be buried.
func TestOrchestrator_ThinCorePartition(t *testing.T) {
	base := orchestratorBaseRegistry()
	l := makeLoop(t, &stubProvider{name: "stub"}, OrchestratorRegistry(base), "SYS")
	l.route = RouteCoordinator
	l.thinTools = true
	l.orchestrator = true

	schema, tail := l.thinPartition()
	inSchema := map[string]bool{}
	for _, tl := range schema {
		inSchema[tl.Name] = true
	}
	inTail := map[string]bool{}
	for _, tl := range tail {
		inTail[tl.Name] = true
	}

	for _, core := range orchestratorCoreTools {
		if !inSchema[core] {
			t.Errorf("orchestrator core tool %q not schema-carrying", core)
		}
	}
	if !inSchema["task"] {
		t.Error("task must be schema-core in orchestrator mode (turn-1 primary action)")
	}
	// The delegation continuation + interaction tools ride in the tail.
	for _, tailTool := range []string{"send_message", "task_stop", "ask_user", "goal", "remember"} {
		if !inTail[tailTool] {
			t.Errorf("expected %q in the advertised tail, not schema-core", tailTool)
		}
	}
	// A mutating tool never appears at all (not in the registry).
	if inSchema["edit_line"] || inTail["edit_line"] {
		t.Error("edit_line must be absent from an orchestrator loop entirely")
	}
}

// TestOrchestrator_SchemaCoreIsLighter documents that the orchestrator
// schema-core (task + read-only set) is lighter than the normal thin-core
// (which additionally carries ctx_execute + edit_line). This is the
// token win the mode is expected to deliver; it also guards against a
// future edit accidentally pulling a heavy mutating tool into the core.
func TestOrchestrator_SchemaCoreIsLighter(t *testing.T) {
	// The orchestrator core must not contain the two heaviest mutating
	// tools; if it did, the mode would no longer be lighter.
	for _, banned := range []string{"ctx_execute", "edit_line", "write_file"} {
		if isOrchestratorCore(banned) {
			t.Errorf("%q must not be in orchestratorCoreTools", banned)
		}
	}
	if !isOrchestratorCore("task") {
		t.Error("task must be in orchestratorCoreTools")
	}
}

// TestOrchestrator_OffModeUsesThinCore: with orchestrator off, the loop
// falls back to the historical thinCoreTools decision, unchanged.
func TestOrchestrator_OffModeUsesThinCore(t *testing.T) {
	l := makeLoop(t, &stubProvider{name: "stub"}, tools.NewRegistry(), "SYS")
	if l.isSchemaCore("edit_line") != isThinCore("edit_line") {
		t.Error("orchestrator-off isSchemaCore must equal isThinCore")
	}
	if l.isSchemaCore("task") {
		t.Error("task is not thin-core when orchestrator is off")
	}
}
