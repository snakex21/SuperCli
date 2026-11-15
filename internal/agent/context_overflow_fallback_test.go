package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// A request that cannot fit the window even after pruning ends with a
// host-written answer (what changed, why it stopped), never an empty reply.
func TestContextOverflowEndsWithAnAnswer(t *testing.T) {
	root := t.TempDir()
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name: "patch_file", Description: "edit", Schema: `{"type":"object","properties":{"path":{"type":"string"}}}`,
		Fn: func(_ context.Context, args json.RawMessage) (tools.Result, error) {
			var a struct{ Path string }
			_ = json.Unmarshal(args, &a)
			if err := os.WriteFile(filepath.Join(root, a.Path), []byte("x\n"), 0o644); err != nil {
				return tools.Result{}, err
			}
			return tools.Result{Text: "Patched " + a.Path + "\n" + strings.Repeat("diff line\n", 6000), ChangedPaths: []string{a.Path}}, nil
		},
	})
	reg.MarkAlwaysOn("patch_file")
	main := &stubProvider{name: "main", scripts: [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "c1", Name: "patch_file", Arguments: `{"path":"a.txt"}`}}, {FinishReason: "tool_calls"}},
		{{Content: "should never be requested"}, {FinishReason: "stop"}},
	}}
	l, err := NewLoop(LoopConfig{Provider: main, Registry: reg, BaseDir: root, MaxSteps: 10, CompletionCheck: true})
	if err != nil {
		t.Fatal(err)
	}
	// The window shrinks after the first call (e.g. the server reloaded the
	// model with a smaller context): the second request cannot fit.
	l.windowFor = func(string) int {
		if atomic.LoadInt32(&main.calls) >= 1 {
			return 600
		}
		return 100000
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ch, err := l.Run(ctx, "patch a.txt")
	if err != nil {
		t.Fatal(err)
	}
	var answer string
	var runErr error
	for ev := range ch {
		switch e := ev.(type) {
		case MessageEvent:
			answer += e.Text
		case ErrorEvent:
			runErr = e.Err
		}
	}
	if !isContextStillTooLarge(runErr) {
		t.Fatalf("want the context-window error, got %v", runErr)
	}
	for _, want := range []string{"I had to stop before finishing", "a.txt", "window 600)"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("answer lacks %q: %q", want, answer)
		}
	}
}
