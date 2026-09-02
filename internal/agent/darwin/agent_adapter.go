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
// Worktree isolation: when buildRegistry is non-nil
// and the LoopConfig carries a Home (the agent's git
// worktree path, or the user's cwd when worktrees are
// disabled), each child gets its OWN tool registry
// rooted at that path — file tools (read/edit/
// file_ops/...) resolve inside the worktree, so the
// candidate's changes land on its branch and can be
// diffed/judged/merged. When buildRegistry is nil
// (or fails), the child falls back to the shared
// parent registry — the pre-isolation behavior.
type RegistryBuilder func(root string) (*tools.Registry, error)

func AgentLoopAdapter(registry *tools.Registry, buildRegistry RegistryBuilder) LoopFactory {
	return func(cfg LoopConfig) (Loop, error) {
		reg := registry
		if buildRegistry != nil && cfg.Home != "" {
			if r, err := buildRegistry(cfg.Home); err == nil && r != nil {
				reg = r
			}
		}
		loop, err := agent.NewLoop(agent.LoopConfig{
			Provider:        cfg.Provider,
			Registry:        reg,
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
			case agent.ReasoningEvent:
				chunk := "<thinking>" + e.Text + "</thinking>\n"
				text.WriteString(chunk)
				select {
				case out <- LoopMessageEvent{Text: chunk}:
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
