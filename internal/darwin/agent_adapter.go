package darwin

import (
	"context"
	"strings"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/tools"
)

// AgentLoopAdapter returns a LoopFactory that
// builds real *agent.Loop values from darwin's
// structural LoopConfig. The factory is the only
// place where the darwin package is allowed to
// bridge to internal/agent — keeping the
// darwin->agent import direction one-way and
// breaking the tools->darwin->agent->tools cycle.
//
// Event translation rules:
//   - agent.MessageEvent -> darwin.LoopMessageEvent
//     (streamed text deltas; Darwin doesn't need
//     per-step metadata here)
//   - agent.DoneEvent -> darwin.LoopDoneEvent
//     (final accumulated text + Usage)
//   - agent.ErrorEvent -> darwin.LoopErrorEvent
//   - agent.ReflectionEvent / ToolCallEvent /
//     ToolResultEvent are dropped — Darwin
//     children do not surface tool noise in the
//     pool event stream
//
// Note on worktree isolation: the parent's
// registry is shared with the child, so tools
// that depend on a BaseDir (e.g. read_image) will
// see the parent's home, not the worktree path.
// F6+1 can build a per-worktree registry if
// true per-child file isolation is needed.
func AgentLoopAdapter(registry *tools.Registry) LoopFactory {
	return func(cfg LoopConfig) (Loop, error) {
		loop, err := agent.NewLoop(agent.LoopConfig{
			Provider:        cfg.Provider,
			Registry:        registry,
			System:          cfg.System,
			MaxSteps:        10,
			PatternInjector: nil, // F5 patterns are NOT inherited in F6
		})
		if err != nil {
			return nil, err
		}
		return &agentLoopAdapter{loop: loop}, nil
	}
}

// agentLoopAdapter wraps an *agent.Loop so it
// satisfies the darwin.Loop interface.
type agentLoopAdapter struct {
	loop *agent.Loop
}

// Run starts the agent and returns a channel of
// darwin LoopEvents. The agent's own channel is
// consumed in a goroutine; MessageEvents are
// accumulated so the final LoopDoneEvent has the
// full text.
func (a *agentLoopAdapter) Run(ctx context.Context, prompt string) (<-chan LoopEvent, error) {
	src, err := a.loop.Run(ctx, prompt)
	if err != nil {
		return nil, err
	}
	out := make(chan LoopEvent, 16)
	go func() {
		defer close(out)
		var text strings.Builder
		for ev := range src {
			switch e := ev.(type) {
			case agent.MessageEvent:
				text.WriteString(e.Text)
				select {
				case out <- LoopMessageEvent{Text: e.Text}:
				case <-ctx.Done():
					return
				}
			case agent.DoneEvent:
				u := llm.Usage{
					Input:  e.Usage.Input,
					Output: e.Usage.Output,
					Total:  e.Usage.Total,
				}
				select {
				case out <- LoopDoneEvent{Text: text.String(), Usage: u}:
				case <-ctx.Done():
					return
				}
				return
			case agent.ErrorEvent:
				select {
				case out <- LoopErrorEvent{Err: e.Err}:
				case <-ctx.Done():
					return
				}
				return
			default:
				// agent.ReflectionEvent, agent.ToolCallEvent, etc.
				// are intentionally dropped.
			}
		}
	}()
	return out, nil
}
