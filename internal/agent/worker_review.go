package agent

import (
	"context"
	"fmt"
	"strings"

	"supercli/internal/checkpoint"
)

// Worker review (1.0.39). A worker's "done" means its run ended, not that the
// work is right. Two cheap facts are checked when a file-changing worker says
// done, and its card then shows "needs review" instead of "done":
//
//   - it changed no file at all in this run (a "fixed it" with an empty diff);
//   - the project's verify_commands (config, e.g. "go build ./...") fail
//     after its changes - checked only when such commands are configured.
//
// Review lines are English, one per finding, prefixed "review:"; hosts
// localize them by these exact texts.

const (
	ReviewNoChanges     = "review: no file changes were recorded in this run"
	reviewVerifyFailedF = "review: verify command failed: %s (exit %d)"
)

// reviewRun is the worker's reviewHook.
func (a *AgentTool) reviewRun(ctx context.Context, w *Worker) string {
	if w == nil || w.Loop == nil || !checkpoint.HasCheckpointMutators(w.Loop.registry) {
		return "" // read-only workers (explore, advisor) are not expected to edit
	}
	if w.Loop.runMutations.Load() == 0 {
		return ReviewNoChanges
	}
	if len(a.VerifyCommands) == 0 {
		return ""
	}
	dir := w.Loop.baseDir
	if dir == "" {
		dir = a.sandboxRoot()
	}
	cfg := &DraftVerifyConfig{VerifyCommands: a.VerifyCommands, runCommand: a.verifyRunner}
	res := cfg.runSieve(ctx, dir)
	if res.Green {
		return ""
	}
	out := strings.Join(strings.Fields(res.Output), " ")
	if r := []rune(out); len(r) > 300 {
		out = "…" + string(r[len(r)-300:])
	}
	review := fmt.Sprintf(reviewVerifyFailedF, res.Command, res.Exit)
	if out != "" {
		review += " — " + out
	}
	return review
}

// installReview attaches the review check to a worker this tool runs.
func (a *AgentTool) installReview(w *Worker) {
	if w != nil {
		w.reviewHook = a.reviewRun
	}
}
