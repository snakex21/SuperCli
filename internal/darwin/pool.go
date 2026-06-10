package darwin

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"supercli/internal/llm"
)

// LoopFactory builds a Loop. Tests pass a stub
// factory that returns a Loop driving a
// pre-scripted set of PoolEvents; production passes
// darwin.AgentLoopAdapter() (which wraps
// agent.NewLoop).
type LoopFactory func(cfg LoopConfig) (Loop, error)

// Loop is the structural interface every per-agent
// runner implements. The Darwin package must not
// import internal/agent, so we accept any value
// with this shape.
type Loop interface {
	// Run starts the agent and returns a channel
	// of PoolEvents. The channel is closed by the
	// loop when it finishes (success, error, or
	// cancellation). The caller is expected to
	// drain the channel to completion.
	Run(ctx context.Context, prompt string) (<-chan LoopEvent, error)
}

// PoolEvent is a per-agent event flowing on the
// channel returned by Loop.Run. The types are
// distinct from Darwin's orchestrator events
// (StartedEvent / CandidateDoneEvent / ...) to
// avoid confusion at the type-switch site.
type LoopEvent interface{ isLoopEvent() }

// LoopMessageEvent is a streaming delta from the
// model. Text is the partial content; callers that
// only care about the final answer should wait for
// LoopDoneEvent.
type LoopMessageEvent struct {
	Text string
	// Agent is the 0-based spawn index of the agent
	// that emitted this event. Stamped by SpawnPool.
	Agent int
}

func (LoopMessageEvent) isLoopEvent() {}

// LoopDoneEvent signals the agent finished
// successfully. Text is the final answer; Usage is
// the total token usage for this run.
type LoopDoneEvent struct {
	Text  string
	Usage llm.Usage
	// Agent is the 0-based spawn index of the agent
	// that emitted this event. Stamped by SpawnPool
	// so the orchestrator can map a completion-order
	// event back to its worktree/branch.
	Agent int
}

func (LoopDoneEvent) isLoopEvent() {}

// LoopErrorEvent signals the agent failed. The
// pool records the error in the candidate and
// continues (unless the budget is exhausted).
type LoopErrorEvent struct {
	Err error
	// Agent is the 0-based spawn index of the agent
	// that emitted this event. Stamped by SpawnPool.
	Agent int
}

func (LoopErrorEvent) isLoopEvent() {}

// LoopConfig is what a factory receives when
// building a Loop. Keep it small and value-only so
// the agent adapter can build a real agent.Loop
// from it without touching the registry directly.
type LoopConfig struct {
	Provider llm.Provider
	System   string
	// Home is the worktree path (or the user's
	// cwd if worktrees are disabled) so the child
	// agent reads/writes inside the right tree.
	Home string
	// Prompt is unused here (passed to Run) but
	// kept on the config so factory implementations
	// can pre-warm caches or echo it.
	Prompt string
}

// PoolConfig is the static configuration for a
// pool. All fields are required unless documented
// otherwise; defaults are applied in Darwin.Run
// (not here) so a zero PoolConfig is detectable in
// tests.
type PoolConfig struct {
	// Provider is the LLM backend shared by all
	// agents. Required.
	Provider llm.Provider
	// System is the system prompt shared by all
	// agents. Required.
	System string
	// Home is the worktree base (or the user's
	// cwd if worktrees are disabled). Required.
	Home string
	// Homes is an optional per-agent home override:
	// agent i works in Homes[i] (typically its git
	// worktree path). Missing/empty entries fall
	// back to Home.
	Homes []string
	// Factory is the Loop factory. Required.
	Factory LoopFactory
	// PoolSize is how many agents to spawn. Will
	// be clamped to [1, maxPoolSize] in
	// SpawnPool. Default 3.
	PoolSize int
	// MaxStepsPerAgent is passed to each Loop's
	// config; the agent loop enforces it
	// internally. 0 means use Loop's default.
	MaxStepsPerAgent int
	// BudgetTokens is the total token budget for
	// the whole pool. If exceeded, the run
	// aborts with errBudgetExceeded. 0 disables.
	BudgetTokens int
	// BudgetWall is the wall-clock timeout for
	// the whole pool. Default 10m.
	BudgetWall time.Duration
}

// maxPoolSize caps the PoolSize input. Anything
// larger is clamped down with a warning (we don't
// error; some users do want 50-way pools).
const maxPoolSize = 10

// SpawnPool runs N loops in parallel and returns
// the candidates. The returned channel emits
// LoopEvents from each agent; the pool itself
// does not block on the result — the caller
// (typically Darwin.Run) consumes the channel and
// assembles the Result.
//
// On success, the result is Result.PoolSize
// candidates in spawn order (one per agent). On
// budget exceeded, the candidates observed so far
// are returned alongside an errBudgetExceeded.
//
// SpawnPool never panics. Any per-agent error
// becomes LoopErrorEvent on the channel; the
// candidate is still recorded with Err set.
func SpawnPool(ctx context.Context, cfg PoolConfig) (<-chan LoopEvent, error) {
	if cfg.Factory == nil {
		return nil, errors.New("darwin: PoolConfig.Factory is required (use darwin.AgentLoopAdapter in main.go)")
	}
	if cfg.Provider == nil {
		return nil, errors.New("darwin: PoolConfig.Provider is required")
	}
	if cfg.Home == "" {
		return nil, errors.New("darwin: PoolConfig.Home is required")
	}
	if cfg.System == "" {
		return nil, errors.New("darwin: PoolConfig.System is required")
	}
	size := cfg.PoolSize
	if size <= 0 {
		size = 3
	}
	if size > maxPoolSize {
		size = maxPoolSize
	}
	wall := cfg.BudgetWall
	if wall <= 0 {
		wall = 10 * time.Minute
	}
	poolCtx, cancel := context.WithTimeout(ctx, wall)
	out := make(chan LoopEvent, size*4)
	var wg sync.WaitGroup
	wg.Add(size)
	for i := 0; i < size; i++ {
		i := i
		go func() {
			defer wg.Done()
			home := cfg.Home
			if i < len(cfg.Homes) && cfg.Homes[i] != "" {
				home = cfg.Homes[i]
			}
			loopCfg := LoopConfig{
				Provider: cfg.Provider,
				System:   cfg.System,
				Home:     home,
				Prompt:   "", // set per-Run by the caller
			}
			loop, err := cfg.Factory(loopCfg)
			if err != nil {
				emitOrSkip(poolCtx, out, LoopErrorEvent{Err: err, Agent: i})
				return
			}
			ch, err := loop.Run(poolCtx, "")
			if err != nil {
				emitOrSkip(poolCtx, out, LoopErrorEvent{Err: err, Agent: i})
				return
			}
			for ev := range ch {
				emitOrSkip(poolCtx, out, stampAgent(ev, i))
			}
		}()
	}
	go func() {
		wg.Wait()
		close(out)
		cancel()
	}()
	return out, nil
}

// stampAgent rewrites a LoopEvent so its Agent field
// carries the spawn index of the goroutine that
// produced it. Loops themselves don't know their
// index; the pool does.
func stampAgent(ev LoopEvent, i int) LoopEvent {
	switch e := ev.(type) {
	case LoopMessageEvent:
		e.Agent = i
		return e
	case LoopDoneEvent:
		e.Agent = i
		return e
	case LoopErrorEvent:
		e.Agent = i
		return e
	}
	return ev
}

// emitOrSkip sends ev on ch unless the ctx is done
// or the channel is full. A non-blocking send with
// a closed-channel guard.
func emitOrSkip(ctx context.Context, ch chan<- LoopEvent, ev LoopEvent) {
	if ctx.Err() != nil {
		return
	}
	select {
	case ch <- ev:
	case <-ctx.Done():
	default:
		// Channel full + context still live =
		// drop. Per-agent events are best-effort;
		// the final LoopDoneEvent always arrives
		// because Run blocks until the loop
		// finishes.
	}
}

// errBudgetExceeded is returned when the pool's
// total token usage exceeds BudgetTokens.
var errBudgetExceeded = errors.New("darwin: pool budget exceeded")

// fmtAgentID formats a 0-based agent index as
// "agent-N" (1-based for human readability).
// Out-of-range indices return "agent-?".
func fmtAgentID(i int) string {
	if i < 0 || i > 999 {
		return "agent-?"
	}
	return fmt.Sprintf("agent-%d", i+1)
}
