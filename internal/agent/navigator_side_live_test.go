package agent

import (
	"context"
	"os"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// TestNavigatorSideProvider_Live exercises production parsing/routing on the
// configured small endpoint. It is opt-in and does not modify the workspace.
func TestNavigatorSideProvider_Live(t *testing.T) {
	baseURL, model := os.Getenv("SUPERCLI_LIVE_TASK_BASEURL"), os.Getenv("SUPERCLI_LIVE_TASK_MODEL")
	if baseURL == "" || model == "" {
		t.Skip("live navigator: set SUPERCLI_LIVE_TASK_BASEURL and SUPERCLI_LIVE_TASK_MODEL")
	}
	side, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: baseURL, Model: model, Timeout: 3 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	main := liveNavigatorMain{}
	loop, err := NewLoop(LoopConfig{Provider: main, NavigatorProvider: side, Registry: tools.NewRegistry(),
		System: "Answer briefly.", MaxSteps: 2, EnableNavigator: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	ch, err := loop.Run(ctx, "I need help deciding whether this general design tradeoff is worthwhile.")
	if err != nil {
		t.Fatal(err)
	}
	for ev := range ch {
		if fail, ok := ev.(ErrorEvent); ok {
			t.Fatal(fail.Err)
		}
	}
	if loop.route != RouteAdvisor && loop.route != RouteClarify && loop.route != RouteCoordinator {
		t.Fatalf("unexpected live navigator route %q", loop.route)
	}
}

type liveNavigatorMain struct{}

func (liveNavigatorMain) Name() string { return "navigator-main" }

func (liveNavigatorMain) Complete(context.Context, []llm.Message, []llm.ToolDef) (<-chan llm.Delta, error) {
	ch := make(chan llm.Delta, 2)
	ch <- llm.Delta{Content: "short answer"}
	ch <- llm.Delta{Usage: &llm.Usage{Input: 1, Output: 2, Total: 3}}
	close(ch)
	return ch, nil
}
