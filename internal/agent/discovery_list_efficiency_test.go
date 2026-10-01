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

// A deterministic model-side replay: request three named tools, repair only
// missing discoveries, read real fixture evidence, and finish. It measures
// orchestration overhead, not live-model latency or general model behavior.
type namedDiscoveryReplay struct {
	calls, requestBytes int
	issuedReads         bool
	quality             bool
}

func (p *namedDiscoveryReplay) Name() string { return "named-discovery-replay" }
func (p *namedDiscoveryReplay) Complete(ctx context.Context, messages []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.calls++
	wire, _ := json.Marshal(struct {
		Messages []llm.Message
		Tools    []llm.ToolDef
	}{messages, defs})
	p.requestBytes += len(wire)
	var calls []llm.ToolCall
	if p.calls == 1 {
		calls = []llm.ToolCall{{ID: "discover", Name: "tool_search", Arguments: `{"query":"read_many, search_code, read_context"}`}}
	} else if !p.issuedReads {
		for _, name := range []string{"read_many", "search_code", "read_context"} {
			found := false
			for _, m := range messages {
				if m.Role == llm.RoleTool && m.Name == "tool_search" && strings.Contains(m.Content, `"name":"`+name+`"`) {
					found = true
				}
			}
			if !found {
				calls = append(calls, llm.ToolCall{ID: "repair-" + name, Name: "tool_search", Arguments: fmt.Sprintf(`{"query":%q}`, name)})
			}
		}
		if len(calls) == 0 {
			p.issuedReads = true
			calls = []llm.ToolCall{
				{ID: "batch-read", Name: "read_many", Arguments: `{"reads":"a.go:1-1 | b.go:1-1"}`},
				{ID: "code-search", Name: "search_code", Arguments: `{"query":"needle","include":"*.go","context":0}`},
				{ID: "line-context", Name: "read_context", Arguments: `{"file":"b.go","line":2,"radius":1}`},
			}
		}
	}
	ch := make(chan llm.Delta, len(calls)+1)
	for i := range calls {
		ch <- llm.Delta{ToolCall: &calls[i], FinishReason: "tool_calls"}
	}
	if len(calls) == 0 {
		passed := map[string]bool{}
		for _, m := range messages {
			if m.Role != llm.RoleTool {
				continue
			}
			switch m.Name {
			case "read_many":
				passed[m.Name] = strings.Contains(m.Content, "alpha = 11") && strings.Contains(m.Content, "beta = 17")
			case "search_code":
				passed[m.Name] = strings.Contains(m.Content, "needle = 23")
			case "read_context":
				passed[m.Name] = strings.Contains(m.Content, "beta = 17") && strings.Contains(m.Content, "needle = 23")
			}
		}
		p.quality = passed["read_many"] && passed["search_code"] && passed["read_context"]
		ch <- llm.Delta{Content: fmt.Sprintf("Evidence complete: %v", p.quality), FinishReason: "stop"}
	}
	close(ch)
	return ch, nil
}

func TestNamedDiscoveryAvoidsRepairRound(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprint(thin), func(t *testing.T) {
			root := t.TempDir()
			for name, body := range map[string]string{"a.go": "alpha = 11\n", "b.go": "beta = 17\nneedle = 23\n"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			reg := tools.NewRegistry()
			executions := map[string]int{}
			discoveryBytes := 0
			specs := []tools.Tool{tools.NewReadMany(root).Spec(), tools.NewSearchCode(root).Spec(), tools.NewReadContext(root).Spec(), tools.NewReadLines(root).Spec(), tools.NewCreateFile(root).Spec(), tools.NewPatchFile(root).Spec(), tools.NewCtxExecuteTool(nil, root).Spec(), tools.NewReadDocx(root, 0).Spec(), tools.NewEditDocx(root).Spec(), tools.NewEditXlsx(root).Spec()}
			// Registry functions for the independent reads run in parallel.
			for _, spec := range specs {
				reg.MustRegister(spec)
			}
			search := tools.NewToolSearcher(reg, nil).Spec()
			fn := search.Fn
			search.Fn = func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
				executions["tool_search"]++
				r, e := fn(ctx, args)
				discoveryBytes += len(r.Text)
				return r, e
			}
			reg.MustRegister(search)
			reg.MarkAlwaysOn("tool_search")
			p := &namedDiscoveryReplay{}
			l, err := NewLoop(LoopConfig{Provider: p, Registry: reg, BaseDir: root, ThinTools: thin, StableToolset: true, MaxSteps: 8})
			if err != nil {
				t.Fatal(err)
			}
			drainEvents(t, mustRun(t, l, "Inspect the source fixture using read_many, search_code, and read_context; report the verified values."))
			toolCalls := 0
			for _, m := range l.Messages {
				if m.Role == llm.RoleTool {
					toolCalls++
				}
			}
			t.Logf("provider_requests=%d discovery_calls=%d total_tool_calls=%d discovery_result_bytes=%d cumulative_request_bytes=%d quality=%v", p.calls, executions["tool_search"], toolCalls, discoveryBytes, p.requestBytes, p.quality)
			if !p.quality {
				t.Fatal("final result lost real fixture evidence")
			}
			if p.calls != 3 || executions["tool_search"] != 1 || toolCalls != 4 {
				t.Fatalf("avoidable discovery repair: requests=%d discoveries=%d tools=%d", p.calls, executions["tool_search"], toolCalls)
			}
			for _, name := range []string{"ctx_execute", "edit_docx", "edit_xlsx", "patch_file", "create_file"} {
				if reg.IsActive(name) {
					t.Errorf("unrequested schema activated: %s", name)
				}
			}
		})
	}
}
