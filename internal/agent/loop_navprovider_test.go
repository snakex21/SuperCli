package agent

// Tests for the navigator side-provider: when a small provider is wired
// (LoopConfig.NavigatorProvider, fed by task_model / draft wiring in
// main.go), the route-classification call runs there instead of on the
// main provider — so on a single-slot llama.cpp host the navigator's
// prompt never evicts the coordinator's KV cache. A failing side
// provider degrades to the keyword RouteMap fallback and never breaks
// the turn.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// scriptedNavProvider returns a fixed content string on every call and
// records what it was asked.
type scriptedNavProvider struct {
	name     string
	content  string
	calls    int
	messages []llm.Message
}

func (p *scriptedNavProvider) Name() string { return p.name }
func (p *scriptedNavProvider) Complete(ctx context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	p.calls++
	p.messages = append([]llm.Message(nil), msgs...)
	ch := make(chan llm.Delta, 2)
	go func() {
		defer close(ch)
		ch <- llm.Delta{Content: p.content}
		ch <- llm.Delta{FinishReason: "stop", Usage: &llm.Usage{Input: 1, Output: 1, Total: 2}}
	}()
	return ch, nil
}

// erroringNavProvider fails every Complete call (dead side host).
type erroringNavProvider struct{ calls int }

func (p *erroringNavProvider) Name() string { return "dead-side-host" }
func (p *erroringNavProvider) Complete(context.Context, []llm.Message, []llm.ToolDef) (<-chan llm.Delta, error) {
	p.calls++
	return nil, errors.New("connection refused")
}

// TestLoop_NavigatorUsesSideProvider: with NavigatorProvider set, the
// classification call goes to the side provider (which sees the
// navigator prompt) and the main provider only answers.
func TestLoop_NavigatorUsesSideProvider(t *testing.T) {
	main := &scriptedNavProvider{name: "main", content: "answer"}
	side := &scriptedNavProvider{name: "side", content: `{"mode":"advisor","reason":"conceptual"}`}
	l, err := NewLoop(LoopConfig{
		Provider:          main,
		NavigatorProvider: side,
		Registry:          tools.NewRegistry(),
		System:            "FULL COORDINATOR PROMPT",
		MaxSteps:          5,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	l.navigate = true
	ch, _ := l.Run(context.Background(), "co lepsze na dłuższą metę?")
	drainEvents(t, ch)

	if side.calls != 1 {
		t.Fatalf("side provider calls = %d, want 1 (navigator classification)", side.calls)
	}
	if len(side.messages) == 0 || !strings.HasPrefix(side.messages[0].Content, navigatorSystemPrompt) {
		t.Fatalf("side provider messages = %+v, want navigator system prompt first", side.messages)
	}
	if main.calls != 1 {
		t.Fatalf("main provider calls = %d, want 1 (answer only, no navigator round-trip)", main.calls)
	}
	if l.Route() != RouteAdvisor {
		t.Fatalf("route = %s, want advisor (side navigator chose it)", l.Route())
	}
}

// TestLoop_NavigatorNoSideProvider_MainClassifies: nil NavigatorProvider
// keeps historical behaviour — the main provider takes both the
// classification call and the answer.
func TestLoop_NavigatorNoSideProvider_MainClassifies(t *testing.T) {
	p := &navigatorProvider{name: "main"}
	l := makeLoop(t, p, tools.NewRegistry(), "FULL COORDINATOR PROMPT")
	l.navigate = true
	ch, _ := l.Run(context.Background(), "co lepsze na dłuższą metę?")
	drainEvents(t, ch)

	if p.calls != 2 {
		t.Fatalf("main provider calls = %d, want 2 (navigator + answer)", p.calls)
	}
}

// TestLoop_NavigatorSideProviderError_FallsBackGracefully: a dead side
// provider degrades to the keyword RouteMap fallback — the turn still
// completes on the main provider, which is never asked to classify.
func TestLoop_NavigatorSideProviderError_FallsBackGracefully(t *testing.T) {
	main := &scriptedNavProvider{name: "main", content: "answer"}
	side := &erroringNavProvider{}
	l, err := NewLoop(LoopConfig{
		Provider:          main,
		NavigatorProvider: side,
		Registry:          tools.NewRegistry(),
		System:            "FULL COORDINATOR PROMPT",
		MaxSteps:          5,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	l.navigate = true
	prompt := "co lepsze na dłuższą metę?"
	ch, _ := l.Run(context.Background(), prompt)
	drainEvents(t, ch)

	if side.calls != 1 {
		t.Fatalf("side provider calls = %d, want 1 (attempted, failed)", side.calls)
	}
	if main.calls != 1 {
		t.Fatalf("main provider calls = %d, want 1 (answer only — no classification retry on main)", main.calls)
	}
	if want := l.routeMap.Classify(prompt); l.Route() != want {
		t.Fatalf("route = %s, want keyword fallback %s", l.Route(), want)
	}
}
