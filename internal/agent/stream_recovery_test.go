package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestMalformedToolBlockDoesNotHideLaterValidCall(t *testing.T) {
	badBlocks := []string{
		"<tool_call>not a call</tool_call>",
		"«»",
		"<tool_call>bad</tool_call> «» <tool_call>still bad</tool_call>",
	}
	validBlocks := []string{"<tool_call><function=read_lines><parameter=file>a.go</parameter></function></tool_call>", "«read_lines\nfile: a.go»"}
	for _, bad := range badBlocks {
		for _, valid := range validBlocks {
			full := "before " + bad + " between " + valid + " after"
			expected := "before " + bad + " between  after"
			// Every byte boundary covers split XML tags and split UTF-8 guillemets.
			for split := 0; split <= len(full); split++ {
				l := makeLoop(t, &stubProvider{name: "stub"}, tools.NewRegistry(), "base")
				stream := make(chan llm.Delta, 3)
				stream <- llm.Delta{Content: full[:split]}
				stream <- llm.Delta{Content: full[split:]}
				stream <- llm.Delta{Usage: &llm.Usage{Input: 8, Output: 3, Total: 11}, FinishReason: "stop"}
				close(stream)
				out := make(chan Event, len(full)+4)
				text, calls, usage, err := l.consume(context.Background(), stream, out)
				close(out)
				if err != nil {
					t.Fatal(err)
				}
				if len(calls) != 1 || calls[0].Name != "read_lines" || decodeArgs(t, calls[0].Arguments)["file"] != "a.go" {
					t.Fatalf("bad=%q valid=%q split=%d: later valid call lost or changed: %+v", bad, valid, split, calls)
				}
				var streamed strings.Builder
				for ev := range out {
					if message, ok := ev.(MessageEvent); ok {
						streamed.WriteString(message.Text)
					}
				}
				if text != expected || streamed.String() != expected {
					t.Fatalf("split=%d text=%q streamed=%q want %q", split, text, streamed.String(), expected)
				}
				if usage == nil || usage.Total != 11 {
					t.Fatalf("usage lost: %+v", usage)
				}
			}
		}
	}
}

func TestLoopRecoversValidToolAfterMalformedBlockWithoutAnotherGeneration(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, tc := range []struct {
			name, block string
		}{
			{"XML", "<tool_call>not a call</tool_call>\n<tool_call><function=read_lines><parameter=file>a.go</parameter></function></tool_call>"},
			{"sentinel", "«»\n«read_lines\nfile: a.go»"},
		} {
			t.Run(fmt.Sprintf("thin=%v/%s", thin, tc.name), func(t *testing.T) {
				p := &stubProvider{name: "recovery", scripts: [][]llm.Delta{
					{{Content: tc.block, FinishReason: "stop"}},
					{{Content: "Finished.", FinishReason: "stop"}},
				}}
				reg := tools.NewRegistry()
				executions := 0
				reg.MustRegister(tools.Tool{Name: "read_lines", Description: "Read fixture source", ReadOnly: true, Schema: `{"type":"object","properties":{"file":{"type":"string"}},"required":["file"]}`, Fn: func(_ context.Context, args json.RawMessage) (tools.Result, error) {
					var parsed struct {
						File string `json:"file"`
					}
					if err := json.Unmarshal(args, &parsed); err != nil {
						return tools.Result{}, err
					}
					if parsed.File != "a.go" {
						return tools.Result{}, fmt.Errorf("wrong file %q", parsed.File)
					}
					executions++
					return tools.Result{Text: "current source"}, nil
				}})
				reg.MarkAlwaysOn("read_lines")
				l, err := NewLoop(LoopConfig{Provider: p, Registry: reg, ThinTools: thin})
				if err != nil {
					t.Fatal(err)
				}
				eventsCh, err := l.Run(context.Background(), "Inspect a.go.")
				if err != nil {
					t.Fatal(err)
				}
				events := drainEvents(t, eventsCh)
				for _, ev := range events {
					if e, ok := ev.(ErrorEvent); ok {
						t.Fatal(e.Err)
					}
				}
				if executions != 1 || p.calls != 2 {
					t.Fatalf("executions=%d model requests=%d, want 1 execution and 2 requests", executions, p.calls)
				}
				if _, ok := events[len(events)-1].(DoneEvent); !ok {
					t.Fatal("missing final answer")
				}
				results := 0
				for _, m := range p.reqs[1] {
					if m.Role == llm.RoleTool && m.Content == "current source" {
						results++
					}
				}
				if results != 1 {
					t.Fatalf("next request received %d results, want 1", results)
				}
			})
		}
	}
}

func TestMalformedToolBlocksRemainTextAndDoNotExecute(t *testing.T) {
	for _, text := range []string{
		"before <tool_call>not a call</tool_call> after",
		"before «» after",
		"<tool_call>not a call</tool_call> «» trailing",
		"<tool_call>not a call</tool_call> <tool_call><function=read_lines>",
	} {
		l := makeLoop(t, &stubProvider{name: "stub"}, tools.NewRegistry(), "base")
		stream := make(chan llm.Delta, len(text))
		for i := range len(text) {
			stream <- llm.Delta{Content: text[i : i+1]}
		}
		close(stream)
		out := make(chan Event, len(text)+4)
		got, calls, _, err := l.consume(context.Background(), stream, out)
		if err != nil || got != text || len(calls) != 0 {
			t.Fatalf("got=%q calls=%+v err=%v want=%q", got, calls, err, text)
		}
	}
}
