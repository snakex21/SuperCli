package agent

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/ctxexec"
)

func TestRealCommandCancellationDoesNotCountAsFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := t.TempDir()
	runner := ctxexec.New(root)
	// Cancel inside the actual runner, after dispatch, without a sleep or poll.
	runner.Now = func() time.Time { cancel(); return time.Now() }
	reg := tools.NewRegistry()
	reg.MustRegister(tools.NewCtxExecuteTool(runner, root).Spec())
	loop, err := NewLoop(LoopConfig{Provider: echoProvider("test"), Registry: reg, BaseDir: root})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{
		"command":    []string{os.Args[0], "-test.run=^$"},
		"timeout_ms": 10000,
	})
	if err != nil {
		t.Fatal(err)
	}
	call := llm.ToolCall{ID: "cancelled-command", Name: "ctx_execute", Arguments: string(raw)}
	result := loop.invoke(ctx, call, make(chan Event, 4))
	if !result.failed || len(result.followUps) != 1 ||
		!strings.Contains(result.followUps[0].Content, "TOOL_OUTCOME_UNKNOWN") {
		t.Fatalf("interruption was reported as a command error: %+v", result)
	}
	if loop.identicalFails.attempts(call.Name, call.Arguments) != 0 {
		t.Fatal("interrupted command polluted the retry gate")
	}
	if loop.concreteFailure.Load() {
		t.Fatal("interruption became failed verification evidence")
	}
}
