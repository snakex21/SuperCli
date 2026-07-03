package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// passthroughFactory builds a real child Loop forwarding the loop
// settings the AgentTool sets (registry, credit tracker, thin/stable
// flags). Unlike childLoopFactory it does NOT drop CreditTracker, so
// the token-budget path can be exercised. It records the created loop
// and its registry for inspection.
func passthroughFactory(reply string, gotLoop **Loop, gotReg *[]string) LoopFactory {
	var mu sync.Mutex
	return func(cfg LoopConfig) (*Loop, error) {
		l, err := NewLoop(LoopConfig{
			Provider:        &stubReplyProvider{name: "child", reply: reply},
			Registry:        cfg.Registry,
			System:          cfg.System,
			MaxSteps:        cfg.MaxSteps,
			InitialMessages: cfg.InitialMessages,
			ThinTools:       cfg.ThinTools,
			StableToolset:   cfg.StableToolset,
			BaseDir:         cfg.BaseDir,
			CreditTracker:   cfg.CreditTracker,
		})
		if err != nil {
			return nil, err
		}
		mu.Lock()
		if gotLoop != nil {
			*gotLoop = l
		}
		if gotReg != nil && cfg.Registry != nil {
			*gotReg = cfg.Registry.Names()
		}
		mu.Unlock()
		return l, nil
	}
}

// baseRegistryWithDelegation is a base registry that (like the real
// main.go one) also carries the delegation tools, so we can prove a
// worker never inherits them.
func baseRegistryWithDelegation() *tools.Registry {
	r := newTestBaseRegistry()
	for _, n := range []string{"task", "send_message", "task_stop"} {
		name := n
		r.MustRegister(tools.Tool{
			Name:        name,
			Description: "delegation tool " + name,
			Schema:      `{"type":"object"}`,
			Fn: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
				return tools.Result{Text: "should never run in a worker"}, nil
			},
		})
	}
	return r
}

// TestTask_DefaultAgentWhenOmitted: a bare {"prompt":...} call routes to
// the general worker instead of erroring on a missing agent.
func TestTask_DefaultAgentWhenOmitted(t *testing.T) {
	reg := NewSubAgentRegistry()
	MustRegisterAll(reg, BuiltinSubAgents())
	at, _ := NewAgentTool(reg, nil, newTestBaseRegistry(), &stubReplyProvider{name: "t"}, nil, childLoopFactory("done", nil))

	res, err := at.execute(context.Background(), json.RawMessage(`{"prompt":"count files"}`))
	if err != nil || res.Err != nil {
		t.Fatalf("execute: err=%v resErr=%v", err, res.Err)
	}
	if !strings.Contains(res.Text, "<agent>general</agent>") {
		t.Errorf("expected general worker, got: %q", res.Text)
	}
}

// TestTask_ExpectFoldedIntoPrompt: the optional expect string is added to
// the worker's briefing so the report is shaped by it.
func TestTask_ExpectFoldedIntoPrompt(t *testing.T) {
	reg := NewSubAgentRegistry()
	MustRegisterAll(reg, BuiltinSubAgents())
	var child *Loop
	at, _ := NewAgentTool(reg, nil, newTestBaseRegistry(), &stubReplyProvider{name: "t"}, nil, passthroughFactory("ok", &child, nil))

	_, err := at.execute(context.Background(), json.RawMessage(`{"prompt":"do X","expect":"the line count"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if child == nil {
		t.Fatal("child loop not created")
	}
	var joined string
	for _, m := range child.Messages {
		joined += m.Content + "\n"
	}
	if !strings.Contains(joined, "do X") || !strings.Contains(joined, "Your final report must contain: the line count") {
		t.Errorf("worker prompt missing expect: %q", joined)
	}
}

// TestTask_WorkerCannotNest: a worker inheriting the full tool set must
// never receive the delegation tools (depth limit 1).
func TestTask_WorkerCannotNest(t *testing.T) {
	reg := NewSubAgentRegistry()
	MustRegisterAll(reg, BuiltinSubAgents()) // general = inherit all
	var childNames []string
	at, _ := NewAgentTool(reg, nil, baseRegistryWithDelegation(), &stubReplyProvider{name: "t"}, nil, passthroughFactory("ok", nil, &childNames))

	_, err := at.execute(context.Background(), json.RawMessage(`{"prompt":"anything"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, n := range childNames {
		if n == "task" || n == "send_message" || n == "task_stop" {
			t.Errorf("worker inherited delegation tool %q; child tools = %v", n, childNames)
		}
	}
	// It should still have the ordinary inherited tools.
	if len(childNames) == 0 {
		t.Error("worker got an empty tool set")
	}
}

// TestTask_TokenBudgetStopsWorker: with a tiny MaxTokens the worker stops
// after the first turn and returns a partial report with a failed status.
func TestTask_TokenBudgetStopsWorker(t *testing.T) {
	reg := NewSubAgentRegistry()
	MustRegisterAll(reg, BuiltinSubAgents())
	at, _ := NewAgentTool(reg, nil, newTestBaseRegistry(), &stubReplyProvider{name: "t"}, nil, passthroughFactory("partial answer", nil, nil))
	at.MaxTokens = 1 // any turn (5 in / 3 out) blows the cap

	res, _ := at.execute(context.Background(), json.RawMessage(`{"prompt":"work forever"}`))
	if res.Err == nil {
		t.Fatal("expected a budget error")
	}
	if !strings.Contains(res.Text, "<status>failed</status>") {
		t.Errorf("expected failed status, got: %q", res.Text)
	}
	if !strings.Contains(res.Text, "token budget exceeded") {
		t.Errorf("expected budget reason in summary, got: %q", res.Text)
	}
	if !strings.Contains(res.Text, "partial answer") {
		t.Errorf("expected partial report text, got: %q", res.Text)
	}
}

// TestTask_SummaryHasStepsAndTokens: the report status line carries the
// worker's step and token cost so a coordinator can relay it.
func TestTask_SummaryHasStepsAndTokens(t *testing.T) {
	reg := NewSubAgentRegistry()
	MustRegisterAll(reg, BuiltinSubAgents())
	at, _ := NewAgentTool(reg, nil, newTestBaseRegistry(), &stubReplyProvider{name: "t"}, nil, passthroughFactory("done", nil, nil))

	res, _ := at.execute(context.Background(), json.RawMessage(`{"prompt":"quick task"}`))
	if !strings.Contains(res.Text, "steps") || !strings.Contains(res.Text, "tok") {
		t.Errorf("summary missing steps/tokens: %q", res.Text)
	}
}

// TestTask_ParentHistoryIsolation drives a parent loop through a real task
// tool call and asserts the parent's message history contains ONLY the
// coordinator-level turns plus the single tool-result notification — never
// the worker's own internal messages.
func TestTask_ParentHistoryIsolation(t *testing.T) {
	parentProv := &scriptedToolCallProvider{
		name:     "parent",
		toolName: "task",
		toolArgs: `{"prompt":"go read the secret file and report"}`,
		final:    "acknowledged",
		toolID:   "call_task_1",
	}

	base := tools.NewRegistry()
	subReg := NewSubAgentRegistry()
	MustRegisterAll(subReg, []SubAgent{
		{Name: "general", Description: "worker", System: "You are a sub-agent worker.", MaxSteps: 3},
	})
	at, err := NewAgentTool(
		subReg, nil, base,
		&scriptedToolCallProvider{name: "child", final: "child-ok"}, nil,
		func(cfg LoopConfig) (*Loop, error) { return NewLoop(cfg) },
	)
	if err != nil {
		t.Fatalf("NewAgentTool: %v", err)
	}
	if err := base.Register(at.Spec()); err != nil {
		t.Fatalf("register task: %v", err)
	}

	loop, err := NewLoop(LoopConfig{
		Provider: parentProv,
		Registry: base,
		System:   "coordinator voice",
		MaxSteps: 5,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	drainEvents(t, mustRun(t, loop, "delegate reading the file"))

	// The worker's briefing must never appear as a standalone parent
	// message; it lives only inside the worker's own (separate) loop.
	var toolResults int
	for _, m := range loop.Messages {
		if m.Role == llm.RoleUser && strings.Contains(m.Content, "go read the secret file") {
			t.Errorf("worker briefing leaked into parent history: %q", m.Content)
		}
		if m.Role == llm.RoleTool {
			toolResults++
			if !strings.Contains(m.Content, "<task-notification>") {
				t.Errorf("tool result is not a task-notification: %q", m.Content)
			}
		}
	}
	if toolResults != 1 {
		t.Errorf("expected exactly one tool-result message in parent history, got %d", toolResults)
	}
}

func mustRun(t *testing.T, l *Loop, prompt string) <-chan Event {
	t.Helper()
	ch, err := l.Run(context.Background(), prompt)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return ch
}
