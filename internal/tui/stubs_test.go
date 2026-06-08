package tui

import (
	"context"

	"supercli/internal/agent"
	"supercli/internal/llm"
)

// newStubAgent returns an Agent whose Name returns the given string.
// Used only by tests; lives here so the test file does not need to
// import the real agent package surface.
func newStubAgent(name string) (agent.Agent, error) {
	return stubAgent{n: name}, nil
}

type stubAgent struct{ n string }

func (s stubAgent) Name() string { return s.n }
func (s stubAgent) Run(ctx context.Context, p string) (<-chan agent.Event, error) {
	ch := make(chan agent.Event, 1)
	ch <- agent.DoneEvent{}
	close(ch)
	return ch, nil
}

// scriptedAgent emits the given events in order, then closes.
type scriptedAgent struct {
	n      string
	events []agent.Event
}

func (s scriptedAgent) Name() string { return s.n }
func (s scriptedAgent) Run(ctx context.Context, p string) (<-chan agent.Event, error) {
	ch := make(chan agent.Event, len(s.events))
	go func() {
		defer close(ch)
		for _, e := range s.events {
			select {
			case ch <- e:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func newStubLLM(name string) (llm.Provider, error) {
	return stubLLM{n: name}, nil
}

type stubLLM struct{ n string }

func (s stubLLM) Name() string { return s.n }
func (s stubLLM) Complete(ctx context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	out := make(chan llm.Delta, 2)
	go func() {
		defer close(out)
		out <- llm.Delta{Role: llm.RoleAssistant, Content: "ok", FinishReason: "stop"}
	}()
	return out, nil
}
