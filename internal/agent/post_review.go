package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"supercli/internal/llm"
)

// PostReviewConfig turns on the interactive post-work review (config
// `review_model`, "Sprawdzanie"): when a Run changed files, the review model
// judges the user's task + the changes + verify_commands evidence in ONE
// tool-free call before the Run completes. accept finishes normally;
// revise/takeover send the reviewer's notes back to the main model (no
// takeover in interactive mode). Nil on LoopConfig = off, zero cost.
type PostReviewConfig struct {
	Provider       llm.Provider
	VerifyCommands []string
	// MaxRounds caps how many times notes are sent back per Run. 0 = 1.
	MaxRounds int
}

// postReviewState is the per-Run review bookkeeping. The snapshot is taken
// lazily right before the first file-changing tool, so read-only Runs never
// walk the tree. Guarded by mu: tool batches may execute concurrently.
type postReviewState struct {
	mu       sync.Mutex
	task     string
	mutated  bool
	snapshot map[string]string
	rounds   int
}

func (l *Loop) postReviewRoot() string {
	if l.baseDir != "" {
		return l.baseDir
	}
	wd, _ := os.Getwd()
	return wd
}

// resetPostReview clears the per-Run review state at Run start.
func (l *Loop) resetPostReview(task string) {
	l.postReview.mu.Lock()
	l.postReview.task = task
	l.postReview.mutated = false
	l.postReview.snapshot = nil
	l.postReview.rounds = 0
	l.postReview.mu.Unlock()
}

// notePostReviewMutation is called right before a tool that may change files
// runs. The first call of a Run snapshots the tree (the "before" side).
func (l *Loop) notePostReviewMutation() {
	if l.reviewCfg == nil || l.reviewCfg.Provider == nil {
		return
	}
	l.postReview.mu.Lock()
	defer l.postReview.mu.Unlock()
	if l.postReview.mutated {
		return
	}
	l.postReview.mutated = true
	l.postReview.snapshot = SnapshotTree(l.postReviewRoot())
}

// runPostReview runs the review at the would-be DoneEvent point. It returns
// true when the reviewer's notes were appended and the main model must work
// again (stepContinue); false means finish the Run as usual.
func (l *Loop) runPostReview(ctx context.Context, out chan<- Event, totalUsage *Usage, canContinue bool) bool {
	cfg := l.reviewCfg
	if cfg == nil || cfg.Provider == nil || ctx.Err() != nil {
		return false
	}
	l.postReview.mu.Lock()
	mutated, before, task, rounds := l.postReview.mutated, l.postReview.snapshot, l.postReview.task, l.postReview.rounds
	l.postReview.mu.Unlock()
	if !mutated {
		return false
	}
	maxRounds := cfg.MaxRounds
	if maxRounds <= 0 {
		maxRounds = 1
	}
	model := llm.ProviderModelName(cfg.Provider)
	if model == "" {
		model = cfg.Provider.Name()
	}
	root := l.postReviewRoot()
	changes := RenderTreeChanges(root, before)
	res := ReviewChange(ctx, cfg.Provider, task, changes, cfg.VerifyCommands, root)
	totalUsage.Input += res.Usage.Input
	totalUsage.Output += res.Usage.Output
	totalUsage.Total += res.Usage.Input + res.Usage.Output
	totalUsage.Cached += res.Usage.CachedInput
	totalUsage.CacheWrite += res.Usage.CacheWrite
	if ctx.Err() != nil {
		return false
	}
	switch {
	case !res.Parsed:
		out <- NoticeEvent{Text: fmt.Sprintf("review (%s): no readable verdict — finished without review", model)}
		return false
	case res.Decision == "accept":
		out <- NoticeEvent{Text: fmt.Sprintf("review (%s): accepted", model)}
		return false
	}
	if rounds >= maxRounds || !canContinue {
		out <- NoticeEvent{Text: fmt.Sprintf("review (%s): %s — round limit reached (%d), finishing", model, res.Decision, maxRounds)}
		return false
	}
	notes := res.ReviseInstruction()
	if res.Decision != "revise" {
		notes = "Your previous attempt was reviewed and the reviewer judged it needs to be redone.\n\n" +
			res.TakeoverEvidence() +
			"Fix the change yourself and confirm the verify commands pass. Report only what you changed."
	}
	l.postReview.mu.Lock()
	l.postReview.rounds++
	round := l.postReview.rounds
	l.postReview.mu.Unlock()
	msg := llm.Message{Role: llm.RoleSystem, Content: "[post-work review] " + strings.TrimSpace(notes)}
	l.Messages = append(l.Messages, msg)
	l.persist(ctx, msg)
	out <- NoticeEvent{Text: fmt.Sprintf("review (%s): %s — notes sent back to the main model (%d/%d)", model, res.Decision, round, maxRounds)}
	return true
}
