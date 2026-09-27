package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestNativeTextReadMirrorExecutesOnce(t *testing.T) {
	for _, nativeFirst := range []bool{true, false} {
		t.Run(fmt.Sprint(nativeFirst), func(t *testing.T) {
			native := llm.Delta{ToolCall: &llm.ToolCall{ID: "native-read", Name: "read_lines", Arguments: "{\"file\":\"a.go\",\"from\":1,\"to\":2}"}}
			text := llm.Delta{Content: "«read_lines\nfile: a.go\nfrom: 1\nto: 2»"}
			first := []llm.Delta{native, text}
			if !nativeFirst {
				first = []llm.Delta{text, native}
			}
			first = append(first, llm.Delta{FinishReason: "stop"})
			provider := &stubProvider{name: "mixed-format", scripts: [][]llm.Delta{first, {{Content: "Finished.", FinishReason: "stop"}}}}
			reg := tools.NewRegistry()
			var executions atomic.Int32
			reg.MustRegister(tools.Tool{Name: "read_lines", Description: "fixture read", ReadOnly: true,
				Schema: "{\"type\":\"object\",\"properties\":{\"file\":{\"type\":\"string\"},\"from\":{\"type\":\"integer\"},\"to\":{\"type\":\"integer\"}},\"required\":[\"file\",\"from\",\"to\"],\"additionalProperties\":false}",
				Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
					executions.Add(1)
					return tools.Result{Text: "1 | package sample\n2 | const Value = 1\n"}, nil
				},
			})
			reg.MarkAlwaysOn("read_lines")
			loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, ThinTools: true})
			if err != nil {
				t.Fatal(err)
			}
			events, err := loop.Run(t.Context(), "Read a.go.")
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range drainEvents(t, events) {
				if e, ok := event.(ErrorEvent); ok {
					t.Fatal(e.Err)
				}
			}
			if executions.Load() != 1 {
				t.Fatalf("same mixed-format read ran %d times", executions.Load())
			}
			if provider.calls != 2 {
				t.Fatalf("unexpected model rounds: %d", provider.calls)
			}
			calls, results := 0, 0
			for _, message := range provider.reqs[1] {
				for _, call := range message.ToolCalls {
					calls++
					if call.ID != "native-read" {
						t.Fatal("native protocol call identity was lost")
					}
				}
				if message.Role == llm.RoleTool {
					results++
					if message.ToolCallID != "native-read" {
						t.Fatal("result does not answer the native call")
					}
				}
			}
			if calls != 1 || results != 1 {
				t.Fatalf("duplicate evidence in next model request: %d calls, %d results", calls, results)
			}
		})
	}
}

func TestReadMirrorsPreserveIndependentCallsAndBarriers(t *testing.T) {
	reg := tools.NewRegistry()
	for _, name := range []string{"read", "write"} {
		reg.MustRegister(tools.Tool{Name: name, Description: "fixture", ReadOnly: name == "read",
			Schema: "{\"type\":\"object\",\"properties\":{\"offset\":{\"type\":\"integer\"}},\"required\":[\"offset\"]}",
			Fn:     func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{Text: "ok"}, nil },
		})
	}
	l := &Loop{registry: reg}
	read := func(id, args string) llm.ToolCall { return llm.ToolCall{ID: id, Name: "read", Arguments: args} }
	native := read("native", "{\"offset\":1}")
	text := read("text", "{\"offset\":\"1\"}")
	for _, tc := range []struct {
		name  string
		calls []llm.ToolCall
		text  []int
		want  []string
	}{
		{"native duplicates remain", []llm.ToolCall{native, read("native-2", native.Arguments)}, nil, []string{"native", "native-2"}},
		{"text duplicates remain", []llm.ToolCall{text, read("text-2", text.Arguments)}, []int{0, 1}, []string{"text", "text-2"}},
		{"different offsets", []llm.ToolCall{native, read("text", "{\"offset\":\"2\"}")}, []int{1}, []string{"native", "text"}},
		{"one mirror per native", []llm.ToolCall{native, text, read("text-2", text.Arguments)}, []int{1, 2}, []string{"native", "text-2"}},
		{"mutation barrier", []llm.ToolCall{text, {ID: "write", Name: "write", Arguments: "{\"offset\":1}"}, native}, []int{0}, []string{"text", "write", "native"}},
		{"unknown barrier", []llm.ToolCall{text, {ID: "unknown", Name: "unknown", Arguments: "{}"}, native}, []int{0}, []string{"text", "unknown", "native"}},
		{"invalid barrier", []llm.ToolCall{text, read("invalid", "{\"offset\":\"bad\"}"), native}, []int{0}, []string{"text", "invalid", "native"}},
		{"mutations are not reads", []llm.ToolCall{{ID: "native", Name: "write", Arguments: "{\"offset\":1}"}, {ID: "text", Name: "write", Arguments: "{\"offset\":\"1\"}"}}, []int{1}, []string{"native", "text"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := l.coalesceMirroredReads(tc.calls, tc.text)
			if len(got) != len(tc.want) {
				t.Fatalf("calls=%+v want IDs=%v", got, tc.want)
			}
			for i, id := range tc.want {
				if got[i].ID != id {
					t.Fatalf("call %d=%s want %s", i, got[i].ID, id)
				}
			}
		})
	}
}

func TestReadMirrorAcrossStreamChunkBoundaries(t *testing.T) {
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "read", Description: "fixture", ReadOnly: true,
		Schema: "{\"type\":\"object\",\"properties\":{\"offset\":{\"type\":\"integer\"}},\"required\":[\"offset\"]}",
		Fn:     func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{Text: "ok"}, nil },
	})
	for _, block := range []string{"«read\noffset: 1»", "<tool_call><function=read><parameter=offset>1</parameter></function></tool_call>"} {
		full := "before " + block + " after"
		for split := 0; split <= len(full); split++ {
			l := &Loop{registry: reg}
			stream := make(chan llm.Delta, 3)
			stream <- llm.Delta{Content: full[:split]}
			stream <- llm.Delta{ToolCall: &llm.ToolCall{ID: "native", Name: "read", Arguments: "{\"offset\":1}"}}
			stream <- llm.Delta{Content: full[split:]}
			close(stream)
			out := make(chan Event, 16)
			prose, calls, _, err := l.consume(t.Context(), stream, out)
			if err != nil || len(calls) != 1 || calls[0].ID != "native" || prose != "before  after" {
				t.Fatalf("split=%d err=%v prose=%q calls=%+v", split, err, prose, calls)
			}
		}
	}
}
