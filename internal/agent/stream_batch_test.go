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

func TestToolBlocksPreserveBatchProseAndUsageAcrossChunkBoundaries(t *testing.T) {
	sentinelRead := "«read_lines\nfile: a.go»"
	sentinelList := "«list_dir\npath: .»"
	xmlRead := "<tool_call><function=read_lines><parameter=file>a.go</parameter></function></tool_call>"
	xmlList := "<tool_call><function=list_dir><parameter=path>.</parameter></function></tool_call>"
	for _, pair := range [][2]string{{sentinelRead, sentinelList}, {xmlRead, xmlList}, {sentinelRead, xmlList}, {xmlRead, sentinelList}} {
		full := "before " + pair[0] + " between " + pair[1] + " after"
		for split := 0; split <= len(full); split++ {
			l := makeLoop(t, &stubProvider{name: "stub"}, tools.NewRegistry(), "base")
			stream := make(chan llm.Delta, 2)
			stream <- llm.Delta{Content: full[:split]}
			stream <- llm.Delta{Content: full[split:], FinishReason: "stop", Usage: &llm.Usage{Total: 9}}
			close(stream)
			out := make(chan Event, 16)
			text, calls, usage, err := l.consume(context.Background(), stream, out)
			close(out)
			if err != nil {
				t.Fatal(err)
			}
			if len(calls) != 2 || calls[0].Name != "read_lines" || calls[1].Name != "list_dir" {
				t.Fatalf("split=%d batch lost/reordered: %+v", split, calls)
			}
			if calls[0].ID == calls[1].ID {
				t.Fatal("duplicate call IDs")
			}
			if decodeArgs(t, calls[0].Arguments)["file"] != "a.go" || decodeArgs(t, calls[1].Arguments)["path"] != "." {
				t.Fatalf("split=%d arguments changed: %+v", split, calls)
			}
			var streamed strings.Builder
			for ev := range out {
				if m, ok := ev.(MessageEvent); ok {
					streamed.WriteString(m.Text)
				}
			}
			if text != "before  between  after" || streamed.String() != text {
				t.Fatalf("split=%d prose lost/leaked/duplicated: text=%q streamed=%q", split, text, streamed.String())
			}
			if usage == nil || usage.Total != 9 {
				t.Fatalf("split=%d usage lost: %+v", split, usage)
			}
		}
	}
}

func TestLoopTextToolBatchExecutesBothCallsInOneProviderRound(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprintf("thin=%v", thin), func(t *testing.T) {
			p := &stubProvider{name: "batch", scripts: [][]llm.Delta{
				{{Content: "«read_lines\nfile: a.go»\n«list_dir\npath: .»", FinishReason: "stop"}},
				{{Content: "Finished.", FinishReason: "stop"}},
			}}
			reg := tools.NewRegistry()
			executed := make(chan string, 2)
			for _, name := range []string{"read_lines", "list_dir"} {
				reg.MustRegister(tools.Tool{Name: name, Description: "read", ReadOnly: true, Schema: `{"type":"object"}`,
					Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
						executed <- name
						return tools.Result{Text: "result for " + name}, nil
					},
				})
				reg.MarkAlwaysOn(name)
			}
			l, err := NewLoop(LoopConfig{Provider: p, Registry: reg, ThinTools: thin})
			if err != nil {
				t.Fatal(err)
			}
			ch, err := l.Run(context.Background(), "Inspect a.go and the directory.")
			if err != nil {
				t.Fatal(err)
			}
			events := drainEvents(t, ch)
			for _, ev := range events {
				if e, ok := ev.(ErrorEvent); ok {
					t.Fatal(e.Err)
				}
			}
			if _, ok := events[len(events)-1].(DoneEvent); !ok {
				t.Fatal("no final answer")
			}
			if len(executed) != 2 || p.calls != 2 {
				t.Fatalf("executions=%d requests=%d", len(executed), p.calls)
			}
			results := 0
			for _, m := range p.reqs[1] {
				if m.Role == llm.RoleTool {
					results++
				}
			}
			if results != 2 {
				t.Fatalf("next request contains %d results, want 2", results)
			}
		})
	}
}
