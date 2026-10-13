package darwin

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	// Prompt is the task the agent will be Run with
	// (the pool passes the same string to Run). Kept on
	// the config so factories can pre-warm caches or
	// echo it.
	Prompt string
	// MaxSteps caps the child loop's iterations. 0
	// means the adapter's default (DefaultMaxStepsPerAgent).
	MaxSteps int
}

// DefaultMaxStepsPerAgent is the step cap a Darwin child
// gets when PoolConfig.MaxStepsPerAgent is 0. Coding tasks
// (read, edit, run tests, fix) routinely need more than a
// handful of tool rounds.
const DefaultMaxStepsPerAgent = 30

// DefaultBudgetWallPerAgent is the per-candidate wall-clock
// limit used when PoolConfig.BudgetWall is 0.
const DefaultBudgetWallPerAgent = 10 * time.Minute

// PoolConfig is the static configuration for a
// pool. All fields are required unless documented
// otherwise; defaults are applied in Darwin.Run
// (not here) so a zero PoolConfig is detectable in
// tests.
type PoolConfig struct {
	// Provider is the LLM backend shared by all
	// agents. Required.
	Provider llm.Provider
	// Providers optionally assigns models to agents
	// round-robin: agent i uses Providers[i%len].
	// Nil entries (or an empty slice) mean Provider.
	Providers []llm.Provider
	// System is the system prompt shared by all
	// agents. Required.
	System string
	// Prompt is the task every agent is Run with.
	// Required: agent.Loop.Run rejects an empty
	// prompt. Darwin.Run fills it in.
	Prompt string
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
	// internally. 0 means DefaultMaxStepsPerAgent.
	MaxStepsPerAgent int
	// BudgetTokens is the total token budget for
	// the whole pool. If exceeded, the run
	// aborts with errBudgetExceeded. 0 disables.
	BudgetTokens int
	// BudgetWall is the wall-clock timeout for EACH
	// candidate (not the pool as a whole): in the
	// sequential path N candidates may take up to
	// N×BudgetWall in total. Default
	// DefaultBudgetWallPerAgent.
	BudgetWall time.Duration
	// Sequential runs the agents one at a time
	// instead of concurrently. On a single local
	// GPU the requests serialize on one server slot
	// anyway (N× wall time), and concurrent contexts
	// thrash each other's KV cache — so best-of-N on
	// a local backend defaults to sequential. Cloud
	// backends keep the parallel path.
	Sequential bool
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
	if strings.TrimSpace(cfg.Prompt) == "" {
		return nil, errors.New("darwin: PoolConfig.Prompt is required")
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
		wall = DefaultBudgetWallPerAgent
	}
	maxSteps := cfg.MaxStepsPerAgent
	if maxSteps <= 0 {
		maxSteps = DefaultMaxStepsPerAgent
	}
	// poolCtx is only cancelled by the caller (or when the pool
	// finishes); the wall-clock budget is applied per agent below.
	poolCtx, cancel := context.WithCancel(ctx)
	out := make(chan LoopEvent, size*4)

	// runAgent drives one agent to completion, emitting its events on
	// out. Shared by the parallel and sequential paths. Every agent
	// that starts produces exactly one terminal event (Done or Error).
	runAgent := func(i int) {
		home := cfg.Home
		if i < len(cfg.Homes) && cfg.Homes[i] != "" {
			home = cfg.Homes[i]
		}
		loopCfg := LoopConfig{
			Provider: cfg.providerFor(i),
			System:   cfg.System,
			Home:     home,
			Prompt:   cfg.Prompt,
			MaxSteps: maxSteps,
		}
		loop, err := cfg.Factory(loopCfg)
		if err != nil {
			emitOrSkip(poolCtx, out, LoopErrorEvent{Err: err, Agent: i})
			return
		}
		agentCtx, agentCancel := context.WithTimeout(poolCtx, wall)
		defer agentCancel()
		ch, err := loop.Run(agentCtx, cfg.Prompt)
		if err != nil {
			emitOrSkip(poolCtx, out, LoopErrorEvent{Err: err, Agent: i})
			return
		}
		terminal := false
		for ev := range ch {
			if isTerminal(ev) {
				if terminal {
					continue // one candidate per agent
				}
				terminal = true
			}
			emitOrSkip(poolCtx, out, stampAgent(ev, i))
		}
		if !terminal {
			// The loop closed its channel without Done/Error —
			// typically the per-agent wall budget fired and the
			// adapter bailed on ctx.Done. Record it as a failed
			// candidate instead of silently losing it.
			errAgent := agentCtx.Err()
			if errors.Is(errAgent, context.DeadlineExceeded) {
				errAgent = fmt.Errorf("darwin: agent exceeded wall budget %s", wall)
			} else if errAgent == nil {
				errAgent = errors.New("darwin: agent finished without a result")
			}
			emitOrSkip(poolCtx, out, LoopErrorEvent{Err: errAgent, Agent: i})
		}
	}

	if cfg.Sequential {
		// One goroutine runs the agents back-to-back: on a single
		// local GPU this is what actually happens on the wire anyway,
		// minus the KV-cache thrashing of interleaved contexts.
		go func() {
			defer close(out)
			defer cancel()
			for i := 0; i < size; i++ {
				if poolCtx.Err() != nil {
					return
				}
				runAgent(i)
			}
		}()
		return out, nil
	}

	var wg sync.WaitGroup
	wg.Add(size)
	for i := 0; i < size; i++ {
		i := i
		go func() {
			defer wg.Done()
			runAgent(i)
		}()
	}
	go func() {
		wg.Wait()
		close(out)
		cancel()
	}()
	return out, nil
}

// providerFor returns the model agent i runs on.
func (cfg PoolConfig) providerFor(i int) llm.Provider {
	if n := len(cfg.Providers); n > 0 && i >= 0 {
		if p := cfg.Providers[i%n]; p != nil {
			return p
		}
	}
	return cfg.Provider
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

// isTerminal reports whether ev ends an agent's stream.
func isTerminal(ev LoopEvent) bool {
	switch ev.(type) {
	case LoopDoneEvent, LoopErrorEvent:
		return true
	}
	return false
}

// emitOrSkip sends ev on ch. Terminal events
// (LoopDoneEvent / LoopErrorEvent) carry the
// candidate and are sent blocking until the
// consumer takes them or ctx is cancelled.
// Progress events (LoopMessageEvent) are
// best-effort: dropped when the channel is full or
// ctx is done.
func emitOrSkip(ctx context.Context, ch chan<- LoopEvent, ev LoopEvent) {
	if ctx.Err() != nil {
		return
	}
	if isTerminal(ev) {
		select {
		case ch <- ev:
		case <-ctx.Done():
		}
		return
	}
	select {
	case ch <- ev:
	case <-ctx.Done():
	default:
		// Channel full + context still live = drop the
		// progress event; it is not material to the result.
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
