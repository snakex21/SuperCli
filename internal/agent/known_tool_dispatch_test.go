package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func knownToolDispatchFixtureTool(name string, fn func(context.Context, json.RawMessage) (tools.Result, error)) tools.Tool {
	return tools.Tool{
		Name: name, Description: "synthetic fixture", ReadOnly: true,
		Schema: "{\"type\":\"object\",\"additionalProperties\":true}",
		Fn:     fn,
		Verify: func(tools.Result) tools.VerifyVerdict { return tools.VerifyVerdict{OK: true} },
	}
}

func knownToolDispatchFixtureRegistry(count int) *tools.Registry {
	reg := tools.NewRegistry()
	fn := func(context.Context, json.RawMessage) (tools.Result, error) {
		return tools.Result{Text: "{\"fixture\":\"ok\"}"}, nil
	}
	if count > 0 {
		reg.MustRegister(knownToolDispatchFixtureTool("lookup", fn))
	}
	for i := 1; i < count; i++ {
		reg.MustRegister(knownToolDispatchFixtureTool(fmt.Sprintf("fixture_%04d", i), fn))
	}
	return reg
}

func TestKnownToolDispatchDynamicSchemaAndCancellation(t *testing.T) {
	reg := knownToolDispatchFixtureRegistry(1)
	executed := 0
	loop := &Loop{registry: reg}
	call := llm.ToolCall{ID: "dynamic-id", Name: "dynamic", Arguments: "{\"value\":\"kept\"}"}
	out := make(chan Event, 32)
	if result := loop.invoke(context.Background(), call, out); !result.failed || !strings.Contains(result.followUps[0].Content, "unknown tool") {
		t.Fatalf("unregistered tool was not rejected: %+v", result)
	}
	tool := knownToolDispatchFixtureTool("dynamic", func(_ context.Context, args json.RawMessage) (tools.Result, error) {
		executed++
		return tools.Result{Text: string(args)}, nil
	})
	tool.Schema = "{\"type\":\"object\",\"properties\":{\"value\":{\"type\":\"string\"}},\"required\":[\"value\"],\"additionalProperties\":false}"
	reg.MustRegister(tool)
	if reg.IsVisible("dynamic") {
		t.Fatal("new tool unexpectedly visible")
	}
	if result := loop.invoke(context.Background(), call, out); result.failed || executed != 1 || result.followUps[0].Content != call.Arguments {
		t.Fatalf("current dormant registration not observed: %+v executed=%d", result, executed)
	}
	bad := call
	bad.Arguments = "{\"unknown\":\"still rejected\"}"
	if result := loop.invoke(context.Background(), bad, out); !result.failed || executed != 1 {
		t.Fatalf("Execute schema validation was bypassed: %+v executed=%d", result, executed)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	call.ID = "cancelled-id"
	if result := loop.invoke(cancelled, call, out); !result.failed || executed != 1 || !strings.Contains(result.followUps[0].Content, "TOOL_NOT_STARTED") {
		t.Fatalf("cancelled dispatch started callback: %+v executed=%d", result, executed)
	}
	close(out)
	for range out {
	}
}

func TestKnownToolDispatchInvokeActivationGuard(t *testing.T) {
	reg := tools.NewRegistry()
	executed := 0
	tool := knownToolDispatchFixtureTool("hidden_mutation", func(context.Context, json.RawMessage) (tools.Result, error) {
		executed++
		return tools.Result{Text: "fixture result"}, nil
	})
	tool.ReadOnly = false
	tool.Schema = "{\"type\":\"object\",\"properties\":{\"value\":{\"type\":\"string\"}},\"additionalProperties\":false}"
	reg.MustRegister(tool)
	reg.MustRegister(NewInvokeTool(reg).Spec())
	loop := &Loop{registry: reg}
	call := llm.ToolCall{ID: "envelope-id", Name: invokeToolName, Arguments: "{\"tool\":\"hidden_mutation\",\"args\":{\"value\":\"kept\"}}"}
	out := make(chan Event, 16)
	if result := loop.invoke(context.Background(), call, out); !result.failed || executed != 0 || !strings.Contains(result.followUps[0].Content, "not active") {
		t.Fatalf("inactive mutation activated/bypassed: %+v executed=%d", result, executed)
	}
	reg.Activate("hidden_mutation")
	if result := loop.invoke(context.Background(), call, out); result.failed || executed != 1 || result.followUps[0].Name != invokeToolName {
		t.Fatalf("active envelope failed protocol pairing: %+v executed=%d", result, executed)
	}
	close(out)
	for range out {
	}
}

type knownToolDispatchFixtureProvider struct {
	call     llm.ToolCall
	requests [][]byte
}

func (p *knownToolDispatchFixtureProvider) Name() string { return "synthetic-name-dispatch" }
func (p *knownToolDispatchFixtureProvider) Complete(_ context.Context, messages []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	raw, err := json.Marshal(struct {
		Messages []llm.Message
		Tools    []llm.ToolDef
	}{messages, defs})
	if err != nil {
		return nil, err
	}
	p.requests = append(p.requests, raw)
	ch := make(chan llm.Delta, 2)
	if len(p.requests) == 1 {
		ch <- llm.Delta{ToolCall: &p.call}
		ch <- llm.Delta{FinishReason: "tool_calls"}
	} else {
		ch <- llm.Delta{Content: "fixture complete", FinishReason: "stop"}
	}
	close(ch)
	return ch, nil
}

func TestKnownToolDispatchPublicLoop(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		call    llm.ToolCall
		target  string
		wantErr string
	}{
		{"exact", llm.ToolCall{ID: "call-1", Name: "lookup", Arguments: " {\"value\":\"kept\"} "}, "lookup", ""},
		{"repair", llm.ToolCall{ID: "call-1", Name: "lookup", Arguments: "{\"value\":\"kept\""}, "lookup", ""},
		{"zen-bash-placeholder", llm.ToolCall{ID: "call-1", Name: "bash", Arguments: "{\"command\":\"synthetic, never executed\"}"}, "ctx_execute", ""},
		{"zen-read-placeholder", llm.ToolCall{ID: "call-1", Name: "read", Arguments: "{\"filePath\":\"synthetic.txt\",\"offset\":1,\"limit\":2}"}, "read_lines", ""},
		{"missing-typo", llm.ToolCall{ID: "call-1", Name: "lookp", Arguments: "{}"}, "lookp", "Did you mean"},
		{"retired-editor", llm.ToolCall{ID: "call-1", Name: "edit_line", Arguments: "{}"}, "edit_line", "no longer exists"},
		{"goal-action", llm.ToolCall{ID: "call-1", Name: "complete_task", Arguments: "{}"}, "complete_task", "action of the"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			reg := knownToolDispatchFixtureRegistry(64)
			executed := 0
			fn := func(_ context.Context, raw json.RawMessage) (tools.Result, error) {
				executed++
				return tools.Result{Text: string(raw)}, nil
			}
			for _, name := range []string{"goal", "patch_file", "tool_search"} {
				reg.MustRegister(knownToolDispatchFixtureTool(name, fn))
			}
			if scenario.target == "lookup" {
				// Lookup already exists: retain the original stable callback and count its result event.
			} else if scenario.wantErr == "" {
				reg.MustRegister(knownToolDispatchFixtureTool(scenario.target, fn))
			}
			reg.Activate("lookup", "ctx_execute", "read_lines")
			p := &knownToolDispatchFixtureProvider{call: scenario.call}
			loop, err := NewLoop(LoopConfig{Provider: p, Registry: reg, System: "synthetic fixture", MaxSteps: 3, SkipImplementationHint: true})
			if err != nil {
				t.Fatal(err)
			}
			ch, err := loop.Run(context.Background(), "run synthetic fixture")
			if err != nil {
				t.Fatal(err)
			}
			var semantics []string
			calls := 0
			for event := range ch {
				switch event := event.(type) {
				case ToolCallEvent:
					calls++
					if event.Name != scenario.target {
						t.Fatalf("target=%q want=%q", event.Name, scenario.target)
					}
					semantics = append(semantics, "call:"+event.ID+":"+event.Name+":"+event.Args)
				case ToolResultEvent:
					if scenario.wantErr != "" {
						if event.Err == nil || !strings.Contains(event.Err.Error(), scenario.wantErr) {
							t.Fatalf("missing original error advice: %v", event.Err)
						}
						semantics = append(semantics, "error:"+event.ID+":"+event.Err.Error())
					} else if event.Err != nil {
						t.Fatal(event.Err)
					}
					semantics = append(semantics, "result:"+event.ID+":"+event.Output)
				case MessageEvent:
					semantics = append(semantics, "text:"+event.Text)
				case ErrorEvent:
					t.Fatal(event.Err)
				}
			}
			if calls != 1 || len(p.requests) != 2 || (scenario.target != "lookup" && scenario.wantErr == "" && executed != 1) || (scenario.wantErr != "" && executed != 0) {
				t.Fatalf("calls=%d requests=%d executed=%d", calls, len(p.requests), executed)
			}
			bundle, err := json.Marshal(struct {
				Requests [][]byte
				History  []llm.Message
				Events   []string
			}{p.requests, loop.Messages, semantics})
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(bundle)
			t.Logf("scenario=%s exact_semantic_bytes=%d sha256=%s", scenario.name, len(bundle), hex.EncodeToString(sum[:]))
		})
	}
}
