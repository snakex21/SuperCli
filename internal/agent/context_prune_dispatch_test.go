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
	"supercli/internal/tools/core"
)

func TestDispatchPruneProcessHelper(t *testing.T) {
	if os.Getenv("SUPERCLI_DISPATCH_PRUNE_HELPER") != "1" {
		return
	}
	fmt.Print(strings.Repeat("completed process evidence\n", 600))
	if os.Getenv("SUPERCLI_DISPATCH_PRUNE_FAIL") == "1" {
		os.Exit(7)
	}
	os.Exit(0)
}

// Discovery and invocation may share a batch, so the initial rewrite must
// wait for tool_search to run. The original wire pair remains invoke_tool.
func TestLateDispatchedProcessPrune(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, exit := range []int{0, 7} {
			t.Run(fmt.Sprintf("thin=%t/exit=%d", thin, exit), func(t *testing.T) {
				root := t.TempDir()
				process := tools.NewProcessSession(root)
				defer process.Close()
				reg := tools.NewRegistry()
				spec := process.Spec()
				execute := spec.Fn
				starts, waits := 0, 0
				spec.Fn = func(ctx context.Context, raw json.RawMessage) (tools.Result, error) {
					var a struct{ Action string }
					_ = json.Unmarshal(raw, &a)
					if a.Action == "start" {
						starts++
					}
					if a.Action == "wait" {
						waits++
					}
					return execute(ctx, raw)
				}
				reg.MustRegister(spec)
				searches := 0
				reg.MustRegister(tools.Tool{Name: "tool_search", Description: "activate process capability", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
					searches++
					reg.ActivateDiscovered("process_session")
					return tools.Result{Text: "process_session is available"}, nil
				}})
				reg.MarkAlwaysOn("tool_search")
				ensureWorkerDiscovery(reg)
				loop, err := NewLoop(LoopConfig{Provider: echoProvider("dispatch-prune"), Registry: reg, BaseDir: root, ThinTools: thin, StableToolset: true})
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				fail := "0"
				if exit != 0 {
					fail = "1"
				}
				raw, _ := json.Marshal(map[string]any{"action": "start", "command": []string{os.Args[0], "-test.run=^TestDispatchPruneProcessHelper$"}, "env": []string{"SUPERCLI_DISPATCH_PRUNE_HELPER=1", "SUPERCLI_DISPATCH_PRUNE_FAIL=" + fail}, "yield_ms": 0})
				started, err := spec.Fn(ctx, raw)
				if err != nil || started.Err != nil {
					t.Fatalf("start: %v %+v", err, started)
				}
				var initial struct{ ID string }
				if err := json.Unmarshal([]byte(started.Text), &initial); err != nil || initial.ID == "" {
					t.Fatalf("missing process ID: %s", started.Text)
				}
				raw, _ = json.Marshal(map[string]any{"tool": "process_session", "args": map[string]any{"action": "wait", "id": initial.ID}})
				call := llm.ToolCall{ID: "late-wait", Name: invokeToolName, Arguments: string(raw)}
				if thin {
					args, _ := json.Marshal(map[string]any{"action": "wait", "id": initial.ID})
					calls, _ := extractSentinelToolCalls("«invoke_tool\ntool: process_session\nargs: " + string(args) + "\n»")
					if len(calls) != 1 {
						t.Fatal("no sentinel call")
					}
					call = calls[0]
					call.ID = "late-wait"
				}
				batch := []llm.ToolCall{{ID: "activate", Name: "tool_search", Arguments: "{}"}, call}
				batch = loop.resolveInvokeToolCalls(batch)
				if batch[1].Name != invokeToolName {
					t.Fatal("fixture target was active before discovery")
				}
				loop.Messages = []llm.Message{{Role: llm.RoleUser, Content: "Run the existing process to completion."}, {Role: llm.RoleAssistant, ToolCalls: batch}}
				ok, outcomes := loop.invokeToolCalls(ctx, batch, make(chan Event, 32))
				if !ok || len(outcomes) != 2 || outcomes[0].failed || outcomes[1].failed != (exit != 0) {
					t.Fatalf("unexpected outcomes: %t %+v", ok, outcomes)
				}
				result := loop.Messages[len(loop.Messages)-1]
				if result.Name != invokeToolName || result.ToolCallID != "late-wait" {
					t.Fatalf("wire result changed: %+v", result)
				}
				original := result.Content
				handle := core.StoredOutputHandle(original)
				loop.Messages = append(loop.Messages, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "recent", Name: "read_lines", Arguments: "{}"}}}, llm.Message{Role: llm.RoleTool, Name: "read_lines", ToolCallID: "recent", Content: strings.Repeat("recent evidence ", 50)})
				// A reload has only transcript fields, not a registry's past activation state.
				encoded, _ := json.Marshal(loop.Messages)
				var restored []llm.Message
				if err := json.Unmarshal(encoded, &restored); err != nil {
					t.Fatal(err)
				}
				loop.Messages = restored
				loop.pruneProtect = 1
				loop.windowFor = func(string) int { return 1000 }
				if loop.maybePruneToolResults(ctx, nil) == 0 {
					t.Fatal("fixture did not prune")
				}
				result = loop.Messages[3]
				status := "done"
				if exit != 0 {
					status = "failed"
				}
				for _, want := range []string{pruneMarkerPrefix, "process_session", "id=" + initial.ID, "status=" + status, fmt.Sprintf("exit_code=%d", exit)} {
					if !strings.Contains(result.Content, want) {
						t.Fatalf("lost %q: %s", want, result.Content)
					}
				}
				if result.Name != invokeToolName || loop.Messages[1].ToolCalls[1].Name != invokeToolName {
					t.Fatal("pruning changed protocol pairing")
				}
				if handle != "" {
					if !strings.Contains(result.Content, handle) {
						t.Fatal("lost output handle")
					}
					raw, _ := json.Marshal(map[string]any{"handle": handle})
					retained, err := reg.Execute(ctx, "read_output", raw)
					if err != nil || retained.Err != nil || !strings.Contains(retained.Text, "completed process evidence") {
						t.Fatalf("retained evidence inaccessible: %v %+v", err, retained)
					}
				}
				found := false
				for _, m := range loop.providerMessages() {
					if m.ToolCallID == "late-wait" {
						found = true
						if m.Content != result.Content {
							t.Fatal("provider lost marker")
						}
					}
				}
				if !found || starts != 1 || waits != 1 || searches != 1 {
					t.Fatalf("found=%t starts=%d waits=%d searches=%d", found, starts, waits, searches)
				}
				t.Logf("one start and blocking wait; %d-byte result -> %d-byte marker", len(original), len(result.Content))
			})
		}
	}
}

func TestPruneDispatchedStatusUsesPairedBatch(t *testing.T) {
	call := llm.ToolCall{ID: "same", Name: invokeToolName, Arguments: `{"tool":"process_session","args":{}}`}
	assistant := llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}}
	body := `{"id":"proc-3","status":"done","exit_code":0,"stdout":"complete"}`
	for _, tc := range []struct {
		name              string
		prefix            []llm.Message
		args, id, content string
		want              bool
	}{
		{name: "paired", prefix: []llm.Message{assistant}, id: "same", content: body, want: true},
		{name: "empty result ID", prefix: []llm.Message{assistant}, content: body},
		{name: "unmatched result ID", prefix: []llm.Message{assistant}, id: "other", content: body},
		{name: "reused in older assistant", prefix: []llm.Message{assistant, {Role: llm.RoleAssistant, Content: "new batch"}}, id: "same", content: body},
		{name: "new user turn", prefix: []llm.Message{assistant, {Role: llm.RoleUser, Content: "New request"}}, id: "same", content: body},
		{name: "ambiguous duplicate", prefix: []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call, call}}}, id: "same", content: body},
		{name: "wrong call name", prefix: []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "same", Name: "read_lines", Arguments: call.Arguments}}}}, id: "same", content: body},
		{name: "file prose", args: `{"tool":"read_lines","args":{}}`, id: "same", content: body},
		{name: "case-sensitive target", args: `{"tool":"read_lines","Tool":"process_session","args":{}}`, id: "same", content: body},
		{name: "missing exact target key", args: `{"Tool":"process_session","args":{}}`, id: "same", content: body},
		{name: "malformed envelope", args: `{"tool":"process_session"} trailing`, id: "same", content: body},
		{name: "array envelope", args: `[{"tool":"process_session"}]`, id: "same", content: body},
		{name: "refused dispatch", prefix: []llm.Message{assistant}, id: "same", content: "error: invoke_tool cannot dispatch process_session\n" + body},
		{name: "image wrapper inside batch", prefix: []llm.Message{assistant, {Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: "Attached image from tool read_image:"}}}}, id: "same", content: body, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			messages := append([]llm.Message(nil), tc.prefix...)
			if tc.args != "" {
				c := call
				c.Arguments = tc.args
				messages = append(messages, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{c}})
			}
			messages = append(messages, llm.Message{Role: llm.RoleTool, Name: invokeToolName, ToolCallID: tc.id, Content: tc.content})
			loop := &Loop{Messages: messages}
			marker := pruneMarker(loop.pruneSourceMessage(len(messages) - 1))
			if got := strings.Contains(marker, "id=proc-3, status=done, exit_code=0"); got != tc.want {
				t.Fatalf("outcome known=%t want=%t: %s", got, tc.want, marker)
			}
			if loop.Messages[len(messages)-1].Name != invokeToolName {
				t.Fatal("source lookup mutated history")
			}
		})
	}
}

func TestLateDispatchedSkillIsNotPrunable(t *testing.T) {
	loop := pruneLoop(t, 1000, 1)
	loop.Messages = []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "skill", Name: invokeToolName, Arguments: `{"tool":"apply_skill","args":{"name":"project"}}`}}},
		{Role: llm.RoleTool, Name: invokeToolName, ToolCallID: "skill", Content: strings.Repeat("important skill guidance ", 1000)},
	}
	if loop.prunable(1) {
		t.Fatal("dispatched skill guidance was treated as disposable output")
	}
}

func BenchmarkToolOutcomePruning(b *testing.B) {
	provider := echoProvider("prune-benchmark")
	bodyBytes, _ := json.Marshal(map[string]any{"id": "proc-1", "status": "done", "exit_code": 0, "stdout": strings.Repeat("process output ", 250)})
	for _, dispatched := range []bool{false, true} {
		b.Run(fmt.Sprintf("dispatched=%t", dispatched), func(b *testing.B) {
			var history []llm.Message
			for i := 0; i < 64; i++ {
				name, args := "process_session", `{"action":"wait","id":"proc-1"}`
				if dispatched {
					name, args = invokeToolName, `{"tool":"process_session","args":{"action":"wait","id":"proc-1"}}`
				}
				id := fmt.Sprintf("call-%d", i)
				history = append(history, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: name, Arguments: args}}}, llm.Message{Role: llm.RoleTool, Name: name, ToolCallID: id, Content: string(bodyBytes)})
			}
			history = append(history, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "recent", Name: "read_lines", Arguments: "{}"}}}, llm.Message{Role: llm.RoleTool, Name: "read_lines", ToolCallID: "recent", Content: strings.Repeat("recent evidence ", 30)})
			b.ReportAllocs()
			for b.Loop() {
				loop := &Loop{provider: provider, modelID: "test", route: RouteCoordinator, windowFor: func(string) int { return 1000 }, pruneProtect: 1, Messages: append([]llm.Message(nil), history...)}
				if loop.maybePruneToolResults(context.Background(), nil) == 0 {
					b.Fatal("benchmark did not prune")
				}
			}
		})
	}
}
