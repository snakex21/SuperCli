package ultrawork

import (
	"context"
	"fmt"
	"sync"
)

// SystemPromptSection returns the extra prompt block appended
// to the system message at the start of an ultrawork Run.
// Kept short and imperative so the model does not drown the
// real instructions.
func SystemPromptSection() string {
	return `

[ULTRAWORK MODE ACTIVE]
You are running in full-autonomy mode. The user typed the
"ultrawork" keyword; they want the job done, not a dialogue.

Operating rules:
- Do NOT ask for confirmation. Make reasonable choices
  and document them in the active /goal notes.
- Decompose work into parallel sub-agents via the
  task tool (explore, plan, review, code) whenever the
  steps are independent. Don't serialize what can be
  parallel.
- For long-running commands (builds, tests, large file
  scans, network requests) use ctx_execute with a long
  timeout; do not busy-poll from the main loop.
- Sisyphus is on: if you stop with unfinished todos on
  the active /goal, the loop will re-prompt you. Don't
  declare done until every task is ` + "`done`" + ` or
  explicitly ` + "`skipped`" + ` via the goal tool.
- Stay focused on the active /goal. Side quests go in
  notes, not in tool calls.`
}

// Sisyphus is the F9 todo-continuation enforcer. It is
// consulted by the agent loop at the end of every turn
// (after the model emits an assistant message with no
// tool calls). When the active /goal still has unfinished
// tasks, Sisyphus returns a system-prompt reminder and the
// loop runs another turn instead of emitting DoneEvent.
//
// Sisyphus caps the number of consecutive re-prompts so a
// stuck model cannot loop forever: after MaxConsecutive
// prompts the enforcer resets and the run is allowed to
// finish. The user can re-issue the prompt (or trim the
// todo list) and try again.
type Sisyphus struct {
	// Goal is consulted for ActiveID and UnfinishedTasks.
	// A nil Goal is a no-op: Sisyphus is effectively off.
	Goal GoalGate

	// MaxConsecutive caps the number of re-prompts in a
	// single Run. Zero means default (3). Negative means
	// the same as zero. There is no upper bound enforced
	// beyond what the caller passes; the agent loop's
	// MaxSteps is the final safety net.
	MaxConsecutive int

	mu              sync.Mutex
	consecutiveHits int
}

// Reset clears the consecutive-hit counter. The agent loop
// calls this when a new Run begins so a previous run's
// stalling does not poison the next one.
func (s *Sisyphus) Reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.consecutiveHits = 0
	s.mu.Unlock()
}

// ShouldContinue inspects the active /goal and returns
// (true, reminder) when there are unfinished tasks and the
// enforcer has not yet hit its cap. Returns (false, "")
// when there is no active goal, no unfinished tasks, the
// goal gate is nil, or the cap has been reached.
//
// The returned reminder is safe to drop verbatim into a
// system message; it includes the current attempt counter
// and the remaining task count so the model can see both.
func (s *Sisyphus) ShouldContinue(ctx context.Context) (bool, string) {
	if s == nil || s.Goal == nil {
		return false, ""
	}
	if s.Goal.ActiveID() == "" {
		// No active goal = nothing to enforce. The Run
		// that led us here must have been started
		// without ultrawork, or the goal was completed
		// mid-run; either way we stop.
		return false, ""
	}
	remaining := s.Goal.UnfinishedTasks(ctx)
	if remaining <= 0 {
		// All tasks done or skipped → model is allowed
		// to stop. Reset so the next run starts clean.
		s.Reset()
		return false, ""
	}
	s.mu.Lock()
	s.consecutiveHits++
	hits := s.consecutiveHits
	s.mu.Unlock()
	max := s.maxConsecutive()
	if hits > max {
		// Cap hit. Reset and let the run finish. The
		// TUI will see DoneEvent; the user can re-prompt
		// after trimming the todo list.
		s.Reset()
		return false, ""
	}
	return true, fmt.Sprintf(
		"[Sisyphus @%d/%d] %d todo(s) still open on the active /goal. "+
			"Continue with the next one. Do NOT declare done until every "+
			"task is `done` or explicitly `skipped` via the goal tool.",
		hits, max, remaining,
	)
}

// consecutiveCap returns the effective MaxConsecutive,
// substituting the default of 3 when zero or negative.
func (s *Sisyphus) maxConsecutive() int {
	if s.MaxConsecutive <= 0 {
		return 3
	}
	return s.MaxConsecutive
}
