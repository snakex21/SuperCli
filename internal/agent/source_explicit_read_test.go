package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func sourceBatchFixture(file, id string, from, to int) (llm.Message, llm.Message) {
	call, result := sourceFixture(file, id, from, to)
	raw, _ := json.Marshal(map[string]any{"reads": fmt.Sprintf("%s:%d-%d", file, from, to)})
	call.ToolCalls[0].Name, call.ToolCalls[0].Arguments = "read_many", string(raw)
	result.Name = "read_many"
	result.Content = fmt.Sprintf("== [1] %s:%d-%d ==\n%s[read_many: 1 ok, 0 failed]", file, from, to, result.Content)
	return call, result
}

// Explicit source/recovery reads must return the requested bytes even when a
// previous search displayed the same lines. Discovery still uses references.
func TestSourceProjectionExplicitReadReturnsTextAfterSearch(t *testing.T) {
	for _, mode := range []string{"native", "nested dispatcher", "flat dispatcher", "read_output"} {
		t.Run(mode, func(t *testing.T) {
			firstCall, first := sourceFixture("a.go", "search", 65, 68)
			firstCall.ToolCalls[0].Name = "search_code"
			firstCall.ToolCalls[0].Arguments = `{"query":"Value","context":3}`
			first.Name, first.Content = "search_code", "== a.go:65-68 ==\n"+first.Content
			readCall, read := sourceFixture("a.go", "explicit", 65, 68)
			switch mode {
			case "nested dispatcher":
				readCall.ToolCalls[0].Name = "invoke_tool"
				readCall.ToolCalls[0].Arguments = `{"tool":"read_lines","args":{"file":"a.go","from":65,"to":68}}`
			case "flat dispatcher":
				readCall.ToolCalls[0].Name = "invoke_tool"
				readCall.ToolCalls[0].Arguments = `{"tool":"read_lines","arg.file":"a.go","arg.from":65,"arg.to":68}`
			case "read_output":
				readCall.ToolCalls[0].Name = "read_output"
				readCall.ToolCalls[0].Arguments = `{"handle":"out_fixture","offset":0,"limit":4096}`
				read.Content = "== a.go:65-68 ==\n" + read.Content
			}
			read.Name = readCall.ToolCalls[0].Name
			againCall, again := readCall, read
			againCall.ToolCalls = append([]llm.ToolCall(nil), readCall.ToolCalls...)
			againCall.ToolCalls[0].ID, again.ToolCallID = "explicit-again", "explicit-again"
			batchCall, batch := sourceBatchFixture("a.go", "batch", 65, 68)
			messages := []llm.Message{firstCall, first, readCall, read, againCall, again, batchCall, batch}
			view := sourceEvidenceProjection(messages, "")
			if view[3].Content != read.Content || view[5].Content != again.Content {
				t.Fatalf("explicit source read suppressed: %s", view[3].Content)
			}
			if !strings.Contains(view[7].Content, "already shown in tool result explicit-again") {
				t.Fatal("discovery did not reference the latest complete visible read")
			}
			if messages[7].Content != batch.Content {
				t.Fatal("projection changed the saved transcript")
			}
		})
	}
}

// Exercise the actual tools, dispatcher/sentinel protocols and provider wire in
// both the main loop and task workers. A second bulk discovery may be shortened;
// an explicit read of the same lines must still reach the model in full.
func TestSourceExplicitReadMainAndWorkerProtocols(t *testing.T) {
	for _, worker := range []bool{false, true} {
		for _, protocol := range []string{"native", "sentinel", "dispatcher"} {
			t.Run(fmt.Sprintf("worker=%t/%s", worker, protocol), func(t *testing.T) {
				root := t.TempDir()
				code := strings.Repeat("// filler\n", 64) + strings.Repeat("const Value = \""+strings.Repeat("x", 60)+"\"\n", 4)
				if err := os.WriteFile(filepath.Join(root, "a.go"), []byte(code), 0600); err != nil {
					t.Fatal(err)
				}
				reg := tools.NewRegistry()
				reg.MustRegister(tools.NewReadLines(root).Spec())
				reg.MustRegister(tools.NewReadMany(root).Spec())
				reg.MustRegister(NewInvokeTool(reg).Spec())
				for _, name := range []string{"read_lines", "read_many", "invoke_tool"} {
					reg.MarkAlwaysOn(name)
				}
				var scripts [][]llm.Delta
				for i, tool := range []string{"read_many", "read_many", "read_lines"} {
					args, fields := `{"reads":"a.go:65-68"}`, "reads: a.go:65-68"
					if tool == "read_lines" {
						args, fields = `{"file":"a.go","from":65,"to":68}`, "file: a.go\nfrom: 65\nto: 68"
					}
					call := llm.ToolCall{ID: fmt.Sprintf("read%d", i), Name: tool, Arguments: args}
					if protocol == "sentinel" {
						scripts = append(scripts, []llm.Delta{{Content: "«" + tool + "\n" + fields + "»", FinishReason: "stop"}})
					} else {
						if protocol == "dispatcher" {
							wrapped, _ := json.Marshal(map[string]any{"tool": tool, "args": json.RawMessage(args)})
							call.Name, call.Arguments = "invoke_tool", string(wrapped)
						}
						scripts = append(scripts, []llm.Delta{{ToolCall: &call}, {FinishReason: "tool_calls"}})
					}
				}
				scripts = append(scripts, []llm.Delta{{Content: "Read the requested lines.", FinishReason: "stop"}})
				provider := &stubProvider{name: "explicit-read", scripts: scripts}
				loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, BaseDir: root, ThinTools: protocol == "sentinel", MaxSteps: 8})
				if err != nil {
					t.Fatal(err)
				}
				if worker {
					specs := NewSubAgentRegistry()
					specs.MustRegister(SubAgent{Name: "code", Description: "read source"})
					task, err := NewAgentTool(specs, loop, reg, provider, nil, NewLoop)
					if err != nil {
						t.Fatal(err)
					}
					result, err := task.execute(context.Background(), json.RawMessage(`{"agent":"code","prompt":"Read a.go lines 65-68 again explicitly."}`))
					if err != nil || result.Err != nil {
						t.Fatalf("worker: %v %v", err, result.Err)
					}
					child, ok := task.Workers.Get("worker-1")
					if !ok {
						t.Fatal("missing task worker")
					}
					loop = child.Loop
				} else {
					for _, event := range drainEvents(t, mustRun(t, loop, "Read a.go lines 65-68 again explicitly.")) {
						if failed, ok := event.(ErrorEvent); ok {
							t.Fatal(failed.Err)
						}
					}
				}
				if len(provider.reqs) != 4 {
					t.Fatalf("expected four requests, got %d", len(provider.reqs))
				}
				var outputs []llm.Message
				for _, message := range provider.reqs[3] {
					if message.Role == llm.RoleTool {
						outputs = append(outputs, message)
					}
				}
				if len(outputs) != 3 || !strings.Contains(outputs[1].Content, "already shown") {
					t.Fatalf("generic discovery deduplication lost: %+v", outputs)
				}
				if strings.Contains(outputs[2].Content, "already shown") || !strings.Contains(outputs[2].Content, "  65 | const Value") || !strings.Contains(outputs[2].Content, "  68 | const Value") {
					t.Fatalf("explicit read lost source on the wire: %s", outputs[2].Content)
				}
				for _, message := range loop.Messages {
					if strings.Contains(message.Content, "already shown in tool result") {
						t.Fatal("canonical history was shortened")
					}
				}
			})
		}
	}
}
