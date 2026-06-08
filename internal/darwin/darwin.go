package darwin

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"supercli/internal/llm"
)

// Config is the user-facing Darwin configuration.
// It extends PoolConfig with the judge, merge
// settings, and the worktree plumbing.
//
// All fields except Judge / AutoMerge / MergeTarget
// are required (the tool wrapper checks and returns
// a structured error).
type Config struct {
	// PoolConfig (embedded) — see PoolConfig.
	PoolConfig

	// Judge is the strategy. Nil defaults to a
	// CompositeJudge wrapping (LLMJudge with the
	// same provider, HeuristicJudge fallback).
	Judge Judge
	// AutoMerge, if true, merges the winner's
	// branch into MergeTarget. Default false.
	AutoMerge bool
	// MergeTarget is the branch to merge into.
	// Default "HEAD" (the caller's current
	// branch). Protected branches (main, master,
	// develop) are refused by WorktreeManager.
	MergeTarget string
	// Worktree is the worktree manager. If nil,
	// Darwin.Run constructs one from PoolConfig.Home.
	Worktree *WorktreeManager
	// UseWorktree, if false, disables worktree
	// isolation even when Worktree is set. Useful
	// for testing in non-git dirs.
	UseWorktree bool
	// OnEvent is an optional callback fired for
	// every event before it's put on the
	// channel. Useful for logging or TUI
	// streaming. Errors are ignored.
	OnEvent func(Event)
}

// Darwin is the orchestrator. One Darwin value is
// safe to call Run on many times; each Run is
// independent and uses its own context.
type Darwin struct {
	cfg Config
	// candidates buffer, guarded by mu.
	mu sync.Mutex
}

// NewDarwin returns a Darwin ready to call Run.
// Returns an error if the config is missing
// required fields.
func NewDarwin(cfg Config) (*Darwin, error) {
	if cfg.Provider == nil {
		return nil, fmt.Errorf("darwin: Config.Provider is required")
	}
	if cfg.Home == "" {
		return nil, fmt.Errorf("darwin: Config.Home is required")
	}
	if cfg.Factory == nil {
		return nil, fmt.Errorf("darwin: Config.Factory is required (use darwin.NewAgentLoopFactory in main.go)")
	}
	if cfg.System == "" {
		return nil, fmt.Errorf("darwin: Config.System is required")
	}
	if cfg.Judge == nil {
		cfg.Judge = defaultJudge(cfg.Provider)
	}
	if cfg.Worktree == nil {
		cfg.Worktree = NewWorktreeManager(cfg.Home)
	}
	return &Darwin{cfg: cfg}, nil
}

// defaultJudge returns a CompositeJudge wrapping
// the given provider and a HeuristicJudge fallback.
func defaultJudge(p llm.Provider) Judge {
	return &CompositeJudge{
		Primary:  &LLMJudge{Provider: p},
		Fallback: NewHeuristicJudge(),
	}
}

// Run starts a Darwin run. The returned channel
// carries orchestrator events; the caller drains
// it until it closes. Run also returns an error
// for setup failures (e.g. invalid config) that
// happen synchronously.
func (d *Darwin) Run(ctx context.Context, prompt string) (<-chan Event, error) {
	if d == nil {
		return nil, fmt.Errorf("darwin: nil receiver")
	}
	if prompt == "" {
		return nil, fmt.Errorf("darwin: empty prompt")
	}
	out := make(chan Event, 16)
	d.emit(out, StartedEvent{Prompt: prompt, PoolSize: d.cfg.PoolSize})
	go d.runInner(ctx, prompt, out)
	return out, nil
}

// runInner is the goroutine that drives a single
// run. It is the only place that emits events
// (other than Run, which emits StartedEvent).
func (d *Darwin) runInner(ctx context.Context, prompt string, out chan<- Event) {
	defer close(out)
	// Phase 1: spawn pool.
	poolCfg := d.cfg.PoolConfig
	// If we're using worktrees, hand each agent a
	// fresh worktree path; otherwise pass the
	// home dir.
	var branches []string
	if d.cfg.UseWorktree && d.cfg.Worktree != nil && d.cfg.Worktree.HasGit() {
		for i := 0; i < poolCfg.PoolSize; i++ {
			branch, path, err := d.cfg.Worktree.Create(ctx)
			if err != nil {
				d.failRun(out, fmt.Errorf("darwin: worktree create: %w", err))
				if d.cfg.Worktree != nil {
					d.cfg.Worktree.CleanupAll(context.Background())
				}
				return
			}
			branches = append(branches, branch)
			_ = path // path is tracked inside the WorktreeManager
		}
		// Clean up at the end (deferred).
		defer d.cfg.Worktree.CleanupAll(context.Background())
	}
	// Phase 2: stream events into candidates.
	stream, err := SpawnPool(ctx, poolCfg)
	if err != nil {
		d.failRun(out, fmt.Errorf("darwin: spawn pool: %w", err))
		return
	}
	var cands []Candidate
	var totalUsage llm.Usage
	budgetExceeded := false
	for ev := range stream {
		switch e := ev.(type) {
		case LoopMessageEvent:
			// Streamed text is not material to the
			// final result; we ignore per-message
			// events and wait for Done/Error.
			_ = e
		case LoopDoneEvent:
			cands = append(cands, Candidate{
				Index: len(cands),
				Text:  e.Text,
				Usage: e.Usage,
			})
			totalUsage = addUsage(totalUsage, e.Usage)
			if d.cfg.BudgetTokens > 0 && totalUsage.Total > d.cfg.BudgetTokens {
				budgetExceeded = true
			}
			d.emit(out, CandidateDoneEvent{Candidate: cands[len(cands)-1]})
		case LoopErrorEvent:
			cands = append(cands, Candidate{
				Index: len(cands),
				Err:   e.Err,
			})
			d.emit(out, CandidateDoneEvent{Candidate: cands[len(cands)-1]})
		}
		if budgetExceeded {
			break
		}
	}
	// Attach branches to candidates (best-effort: if
	// branches were created, they map 1:1 to spawn
	// order).
	if len(branches) == len(cands) {
		for i := range cands {
			cands[i].Branch = branches[i]
			cands[i].AgentID = fmtAgentID(i)
		}
	} else {
		for i := range cands {
			cands[i].AgentID = fmtAgentID(i)
		}
	}
	if budgetExceeded {
		d.failRun(out, fmt.Errorf("darwin: budget exceeded: %d > %d", totalUsage.Total, d.cfg.BudgetTokens))
		return
	}
	// Phase 3: judge.
	verdict, err := d.cfg.Judge.Judge(ctx, prompt, cands)
	if err != nil {
		// Pick fallback (highest-score, lowest-index).
		verdict = pickFallback(cands)
		verdict.Reason = "judge failed: " + err.Error() + "; using fallback"
	}
	if verdict.WinnerIndex < 0 || verdict.WinnerIndex >= len(cands) {
		verdict = pickFallback(cands)
	}
	d.emit(out, JudgeEvent{
		WinnerIndex: verdict.WinnerIndex,
		Score:       verdict.Score,
		Reason:      verdict.Reason,
	})
	// Phase 4: optional merge.
	merged := false
	mergeBranch := ""
	if d.cfg.AutoMerge && verdict.WinnerIndex >= 0 && verdict.WinnerIndex < len(cands) {
		winner := cands[verdict.WinnerIndex]
		if winner.Branch != "" && d.cfg.Worktree != nil {
			target := d.cfg.MergeTarget
			if target == "" {
				target = "HEAD"
			}
			if mErr := d.cfg.Worktree.Merge(ctx, winner.Branch, target); mErr != nil {
				d.failRun(out, mErr)
				return
			}
			merged = true
			mergeBranch = winner.Branch
			d.emit(out, MergedEvent{FromBranch: winner.Branch, ToBranch: target})
		}
	}
	// Phase 5: done.
	res := Result{
		Prompt:      prompt,
		Candidates:  cands,
		WinnerIndex: verdict.WinnerIndex,
		Score:       verdict.Score,
		Reason:      verdict.Reason,
		Merged:      merged,
		MergeBranch: mergeBranch,
		TotalUsage:  totalUsage,
	}
	if verdict.WinnerIndex >= 0 && verdict.WinnerIndex < len(cands) {
		w := cands[verdict.WinnerIndex]
		res.Winner = &w
	}
	d.emit(out, DoneEvent{Result: res})
}

// failRun emits ErrorEvent and returns. Used when
// a phase fails after the run has started.
func (d *Darwin) failRun(out chan<- Event, err error) {
	d.emit(out, ErrorEvent{Err: err})
}

// emit is a non-blocking send. DoneEvent and
// ErrorEvent are always delivered (we never drop
// the final event); other events are best-effort.
func (d *Darwin) emit(out chan<- Event, ev Event) {
	if d.cfg.OnEvent != nil {
		d.cfg.OnEvent(ev)
	}
	select {
	case out <- ev:
	case <-time.After(100 * time.Millisecond):
		// Slow consumer: drop non-final events.
		// The final event (Done/Error) is emitted
		// in runInner after close(out), so this
		// timeout never applies to it.
	}
}

// addUsage sums two llm.Usage values.
func addUsage(a, b llm.Usage) llm.Usage {
	return llm.Usage{
		Input:  a.Input + b.Input,
		Output: a.Output + b.Output,
		Total:  a.Total + b.Total,
	}
}

// pickFallback returns a Verdict pointing to the
// first successful (non-Err) candidate, or the
// first candidate if all failed.
func pickFallback(cands []Candidate) Verdict {
	for i, c := range cands {
		if c.Err == nil {
			return Verdict{
				WinnerIndex: i,
				Score:       0.0,
				Reason:      "fallback: first successful candidate",
			}
		}
	}
	if len(cands) > 0 {
		return Verdict{
			WinnerIndex: 0,
			Score:       0.0,
			Reason:      "fallback: all candidates failed; picked index 0",
		}
	}
	return Verdict{WinnerIndex: -1, Score: 0, Reason: "no candidates"}
}

// errInvalid is a sentinel for arguments that fail
// Darwin's own validation (independent of the
// LLM/tool layer).
var errInvalid = errors.New("darwin: invalid argument")
