// Package darwin implements the Darwin mode: a pool
// of independent agents run in parallel (optionally
// inside isolated git worktrees) whose results are
// evaluated by a judge and merged back into the
// caller's working tree.
//
// The package is designed to break the import cycle
// `tools -> darwin -> agent -> tools` by:
//   - using structural interfaces for the loop
//     abstraction (no agent.* import here)
//   - putting the agent-callable DarwinTool in
//     darwin_tool.go and registering it from main
//     into the tool registry
//   - exposing an AgentLoopAdapter() factory in
//     agent_adapter.go that bridges *agent.Loop to
//     the darwin.Loop structural interface
//
// The orchestrator's events (started / candidate
// done / judge / merged / done / error) are distinct
// from the pool's per-agent events (LoopMessageEvent /
// LoopDoneEvent / LoopErrorEvent) — they have
// different lifecycles and consumers.
package darwin

import (
	"errors"

	"supercli/internal/llm"
)

// Sentinel errors returned by the orchestrator and
// its helpers. Callers can use errors.Is to branch.
var (
	ErrNoGit         = errors.New("darwin: not a git repository (worktree isolation disabled)")
	ErrMergeConflict = errors.New("darwin: merge conflict during auto-merge")
	ErrPoolAborted   = errors.New("darwin: pool aborted")
)

// Candidate is one agent's contribution to a Darwin
// run. The fields mirror what the orchestrator needs
// to (a) forward the candidate to the judge and (b)
// optionally merge the winning candidate back into
// the caller's branch.
type Candidate struct {
	// Index is the 0-based agent number.
	Index int
	// AgentID is the formatted id (e.g. "agent-1").
	AgentID string
	// Branch is the git worktree branch the agent
	// worked on (empty if worktrees were disabled).
	Branch string
	// WorktreePath is the absolute path to the
	// worktree dir (empty if worktrees disabled).
	WorktreePath string
	// Text is the final assistant message.
	Text string
	// Diff is the git diff of the changes the agent
	// made in its worktree (empty if worktrees were
	// disabled or the agent changed nothing). May be
	// truncated for the judge but is stored in full
	// here.
	Diff string
	// Usage is the total token usage for this agent.
	Usage llm.Usage
	// Steps is how many loop iterations the agent
	// took to finish.
	Steps int
	// Err is non-nil if the agent failed.
	Err error
}

// Result is the structured outcome of a Darwin run.
type Result struct {
	// Prompt is the original task description.
	Prompt string
	// Candidates is the list of candidates produced
	// (one per agent), in spawn order.
	Candidates []Candidate
	// WinnerIndex is the 0-based index of the
	// chosen candidate. -1 if no winner.
	WinnerIndex int
	// Winner is a pointer into Candidates.
	Winner *Candidate
	// Score is the judge's 0..1 score for the
	// winner (0 if no judge).
	Score float64
	// Reason is a short explanation of why the
	// judge chose this candidate.
	Reason string
	// Merged is true iff the winner was merged
	// back into the target branch.
	Merged bool
	// MergeBranch is the source branch that was
	// merged (empty if Merged is false).
	MergeBranch string
	// TotalUsage sums token usage across candidates.
	TotalUsage llm.Usage
	// Note carries a human-readable caveat about the
	// run, e.g. that worktree isolation was requested
	// but unavailable because the home dir is not a
	// git repository. Empty when nothing noteworthy.
	Note string
}

// Event is the union of orchestrator events emitted
// on the channel returned by Darwin.Run. Consumers
// type-switch on the concrete types.
type Event interface{ isEvent() }

// StartedEvent is emitted once at the start of a
// Darwin run, before any candidate is spawned.
type StartedEvent struct {
	Prompt   string
	PoolSize int
}

func (StartedEvent) isEvent() {}

// CandidateDoneEvent is emitted when a single
// candidate finishes, whether successfully or with
// an error. The channel may carry several of these
// for a single run.
type CandidateDoneEvent struct {
	Candidate Candidate
}

func (CandidateDoneEvent) isEvent() {}

// JudgeEvent is emitted after all candidates are in
// and the judge has picked a winner.
type JudgeEvent struct {
	WinnerIndex int
	Score       float64
	Reason      string
}

func (JudgeEvent) isEvent() {}

// MergedEvent is emitted when the winner's branch
// is merged into the target branch. If AutoMerge is
// false this event is skipped.
type MergedEvent struct {
	FromBranch string
	ToBranch   string
}

func (MergedEvent) isEvent() {}

// DoneEvent is emitted exactly once at the end of a
// successful run, with the full Result.
type DoneEvent struct {
	Result Result
}

func (DoneEvent) isEvent() {}

// ErrorEvent is emitted exactly once at the end of
// a failed run, with the underlying error.
type ErrorEvent struct {
	Err error
}

func (ErrorEvent) isEvent() {}
