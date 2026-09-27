package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func workerSchemaFixture(t testing.TB) *tools.Registry {
	t.Helper()
	root := t.TempDir()
	reg := tools.NewRegistry()
	for _, tool := range []tools.Tool{
		tools.NewReadLines(root).Spec(),
		tools.NewPatchFile(root).Spec(),
		tools.NewCreateFile(root).Spec(),
		tools.NewCtxExecuteTool(nil, root).Spec(),
		tools.NewReadDocx(root, 0).Spec(),
		tools.NewEditDocx(root).Spec(),
		tools.NewReadXlsx(root, 0).Spec(),
		tools.NewEditXlsx(root).Spec(),
		tools.NewReadPdf(root, 0).Spec(),
		tools.NewReadZip(root, 0).Spec(),
	} {
		reg.MustRegister(tool)
	}
	processTool := tools.NewProcessSession(root)
	t.Cleanup(processTool.Close)
	reg.MustRegister(processTool.Spec())
	reg.MustRegister(tools.NewToolSearcher(reg, nil).Spec())
	return reg
}

func codeWorkerForSchemaTest(t *testing.T, base *tools.Registry, thin bool) *Loop {
	t.Helper()
	specs := NewSubAgentRegistry()
	MustRegisterAll(specs, BuiltinSubAgents())
	provider := &stubReplyProvider{name: "schema-fixture", reply: "done"}
	parent := makeLoop(t, provider, base, "")
	parent.thinTools, parent.stableToolset = thin, true
	var child *Loop
	task, err := NewAgentTool(specs, parent, base, provider, nil, func(cfg LoopConfig) (*Loop, error) {
		var err error
		child, err = NewLoop(cfg)
		return child, err
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := task.execute(context.Background(), json.RawMessage(`{"agent":"code","prompt":"Review and edit a Go function."}`))
	if err != nil || result.Err != nil {
		t.Fatalf("worker: %v %v", err, result.Err)
	}
	return child
}

func TestCodeWorkerDefersUnneededOfficeSchemas(t *testing.T) {
	base := workerSchemaFixture(t)
	child := codeWorkerForSchemaTest(t, base, false)
	defs := child.buildToolDefs()
	wire, _ := json.Marshal(defs)
	t.Logf("native tool definitions: %d tools, %d JSON bytes", len(defs), len(wire))
	names := make(map[string]bool)
	for _, def := range defs {
		names[def.Name] = true
	}
	for _, name := range []string{"read_lines", "patch_file", "create_file", "ctx_execute", "tool_search", "read_output"} {
		if !names[name] {
			t.Errorf("core tool %s requires discovery", name)
		}
	}
	for _, name := range []string{"read_docx", "edit_docx", "read_xlsx", "edit_xlsx", "read_pdf", "read_zip"} {
		if names[name] {
			t.Errorf("unused full schema %s sent on a code turn", name)
		}
		if _, ok := child.registry.Get(name); !ok {
			t.Errorf("deferred capability %s was removed", name)
		}
	}
}

func TestCodeWorkerCanDiscoverDeferredToolsLocally(t *testing.T) {
	base := workerSchemaFixture(t)
	child := codeWorkerForSchemaTest(t, base, false)
	result, err := child.registry.Execute(context.Background(), "tool_search", json.RawMessage(`{"query":"edit_xlsx","limit":1}`))
	if err != nil || result.Err != nil {
		t.Fatalf("discovery: %v %v", err, result.Err)
	}
	if !strings.Contains(result.Text, "edit_xlsx") || !child.registry.IsActive("edit_xlsx") || base.IsActive("edit_xlsx") {
		t.Fatal("deferred tool discovery was missing or changed the parent registry")
	}
	found := false
	for _, def := range child.buildToolDefs() {
		if def.Name == "edit_xlsx" {
			found = true
		}
	}
	if !found {
		t.Fatal("discovered native schema absent from next request")
	}
}

func TestCodeWorkerAllowsProcessSessionsButReadOnlyRolesDoNot(t *testing.T) {
	for _, spec := range BuiltinSubAgents() {
		has := false
		for _, name := range spec.AllowedTools {
			has = has || name == "process_session"
		}
		if spec.Name == "code" && !has {
			t.Fatal("code worker cannot run or inspect long-lived commands")
		}
		if (spec.Name == "review" || spec.Name == "explore" || spec.Name == "plan" || spec.Name == "advisor") && has {
			t.Fatalf("read-only %s gained process execution", spec.Name)
		}
	}
}

func TestCodeWorkerWordToolsRemainReadyForDocumentTurns(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprint(thin), func(t *testing.T) {
			base := workerSchemaFixture(t)
			child := codeWorkerForSchemaTest(t, base, thin)
			drainEvents(t, mustRun(t, child, "Read report.docx and explain its structure."))
			names := make(map[string]bool)
			for _, def := range child.buildToolDefs() {
				names[def.Name] = true
			}
			if !names["read_docx"] || !names["edit_docx"] {
				t.Fatal("Word task acquired an extra discovery round")
			}
		})
	}
}

func TestThinCodeWorkerKeepsDeferredToolCatalog(t *testing.T) {
	base := workerSchemaFixture(t)
	child := codeWorkerForSchemaTest(t, base, true)
	catalog := child.thinToolsPreamble()
	for _, name := range []string{"read_docx", "edit_docx", "read_xlsx", "edit_xlsx", "read_pdf", "read_zip", "process_session"} {
		if !strings.Contains(catalog, name) {
			t.Fatalf("%s disappeared from thin catalog", name)
		}
	}
}

func TestDeferredDiscoveryCannotExposeForbiddenTools(t *testing.T) {
	base := workerSchemaFixture(t)
	for _, name := range []string{"task", "send_message", "task_stop", "private_mutator"} {
		base.MustRegister(tools.Tool{Name: name, Description: name, Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			t.Error("forbidden execution")
			return tools.Result{}, nil
		}})
	}
	child := restrictedRegistry(base, []string{"read_lines", "edit_xlsx", "task", "send_message", "task_stop"}, "edit_xlsx", "private_mutator")
	for _, name := range []string{"task", "send_message", "task_stop", "private_mutator"} {
		if _, ok := child.Get(name); ok {
			t.Fatalf("forbidden %s registered", name)
		}
		query, _ := json.Marshal(map[string]any{"query": name, "limit": 1})
		if _, err := child.Execute(context.Background(), "tool_search", query); err != nil {
			t.Fatal(err)
		}
		if child.IsActive(name) {
			t.Fatalf("forbidden %s activated", name)
		}
	}
}

func TestCodeWorkerCanStartAndAwaitProcess(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprint(thin), func(t *testing.T) {
			base := workerSchemaFixture(t)
			child := codeWorkerForSchemaTest(t, base, thin)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			loaded, err := child.registry.Execute(ctx, "tool_search", json.RawMessage(`{"query":"process_session","limit":1}`))
			if err != nil || loaded.Err != nil || !child.registry.IsActive("process_session") {
				t.Fatalf("discovery: %v %+v", err, loaded)
			}
			invoke := func(args map[string]any) toolResult {
				raw, _ := json.Marshal(args)
				call := llm.ToolCall{ID: "process", Name: "process_session", Arguments: string(raw)}
				if thin {
					raw, _ = json.Marshal(map[string]any{"tool": "process_session", "args": args})
					call.Name, call.Arguments = "invoke_tool", string(raw)
				}
				return child.invoke(ctx, call, make(chan Event, 8))
			}
			start := invoke(map[string]any{"action": "start", "command": []string{os.Args[0], "-test.run=^$"}, "yield_ms": 0})
			if start.failed || len(start.followUps) != 1 {
				t.Fatalf("start: %+v", start)
			}
			var started struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal([]byte(start.followUps[0].Content), &started); err != nil || started.ID == "" {
				t.Fatalf("start output: %+v %v", start, err)
			}
			finished := invoke(map[string]any{"action": "wait", "id": started.ID})
			if finished.failed || len(finished.followUps) != 1 {
				t.Fatalf("wait: %+v", finished)
			}
			var state struct {
				Status   string `json:"status"`
				ExitCode int    `json:"exit_code"`
			}
			if err := json.Unmarshal([]byte(finished.followUps[0].Content), &state); err != nil || state.Status != "done" || state.ExitCode != 0 {
				t.Fatalf("process did not finish normally: %+v %v", finished, err)
			}
			if base.IsActive("process_session") {
				t.Fatal("worker discovery modified parent visibility")
			}
		})
	}
}
