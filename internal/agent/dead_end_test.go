package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestDeadEndClassIgnoresNamesPathsAndNumbers(t *testing.T) {
	a := deadEndClass(`checkpoint before create_file (tool did not run): checkpoint turn no longer accepts mutations`)
	b := deadEndClass(`checkpoint before create_file (tool did not run): checkpoint turn no longer accepts mutations`)
	c := deadEndClass(`write C:\work\a\native_bed_tiles.zig: access denied (code 5)`)
	d := deadEndClass(`write C:\work\b\native_bed_color.zig: access denied (code 5)`)
	if a != b || c != d || a == c {
		t.Fatalf("classes: %q %q %q %q", a, b, c, d)
	}
}

// Worker 312: file changes refused with the same error, each time with other
// arguments, between successful reads, until the 300-step safety net. The
// dead-end stop ends such a worker after deadEndLimit failures with one
// report; the reads in between do not reset the count.
func TestWorkerStopsAtFileChangeDeadEnd(t *testing.T) {
	var scripts [][]llm.Delta
	call := func(id, name string, args map[string]any) []llm.Delta {
		raw, _ := json.Marshal(args)
		return []llm.Delta{{ToolCall: &llm.ToolCall{ID: id, Name: name, Arguments: string(raw)}}, {FinishReason: "tool_calls"}}
	}
	for i := 0; i < 12; i++ {
		scripts = append(scripts, call(fmt.Sprintf("w%d", i), "create_file", map[string]any{"path": fmt.Sprintf("stage/native_bed_%d.zig", i), "content": fmt.Sprintf("const v = %d;\n", i)}))
		scripts = append(scripts, call(fmt.Sprintf("r%d", i), "read_lines", map[string]any{"path": "native_bed.zig"}))
	}
	scripts = append(scripts, []llm.Delta{{Content: "Raport: zapisy plików są odrzucane."}, {FinishReason: "stop"}})
	provider := &stubProvider{name: "dead-end", scripts: scripts}
	writes, reads := 0, 0
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "create_file", Description: "create", Schema: `{"type":"object"}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		writes++
		return tools.Result{Err: errors.New("checkpoint before create_file (tool did not run): checkpoint turn no longer accepts mutations")}, nil
	}})
	reg.MustRegister(tools.Tool{Name: "read_lines", Description: "read", Schema: `{"type":"object"}`, ReadOnly: true, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		reads++
		return tools.Result{Text: fmt.Sprintf("1 | const color = %d;", reads)}, nil
	}})
	l, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, BaseDir: t.TempDir(), MaxSteps: 300})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), workerRunKey{}, true)
	ch, err := l.Run(ctx, "Dokończ łóżko")
	if err != nil {
		t.Fatal(err)
	}
	stopped := false
	for ev := range ch {
		if n, ok := ev.(NoticeEvent); ok && strings.Contains(n.Text, "stopped this worker") {
			stopped = true
		}
	}
	if !stopped {
		t.Fatalf("no dead-end stop after %d refused writes and %d reads", writes, reads)
	}
	if writes != deadEndLimit {
		t.Fatalf("worker kept writing after the dead end: %d refused writes (limit %d), %d reads", writes, deadEndLimit, reads)
	}
	if reads < deadEndLimit-1 {
		t.Fatalf("the reads in between were not interleaved: %d", reads)
	}
	last := l.Messages[len(l.Messages)-1]
	if last.Role != llm.RoleAssistant {
		t.Fatalf("no final report after the stop: %+v", last)
	}
	// A successful file change resets the count.
	var d deadEndTracker
	for i := 0; i < deadEndLimit-1; i++ {
		d.failure("create_file", "access denied")
	}
	d.success()
	d.failure("create_file", "access denied")
	if _, _, _, due := d.due(); due {
		t.Fatal("a successful change did not reset the count")
	}
}
