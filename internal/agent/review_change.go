package agent

import (
	"context"
	"fmt"
	"strings"

	"supercli/internal/llm"
)

// ReviewOutcome is one post-hoc review of a finished change (config
// `review_model`): a cheap model did the work, a stronger model judges the
// result in a single tool-free call. It reuses the draft-verify sieve and
// verdict contract, so accept/revise/takeover mean the same thing there.
type ReviewOutcome struct {
	// Decision is "accept", "revise" or "takeover".
	Decision    string
	Instruction string
	// Parsed is false when the verdict could not be read; Decision is then
	// the safe "takeover" fallback.
	Parsed bool
	Usage  llm.Usage
	sieve  sieveResult
}

// SieveRed reports whether a verify command failed.
func (o ReviewOutcome) SieveRed() bool { return !o.sieve.Green && !o.sieve.Skipped }

// ReviewChange runs verifyCommands in dir, then asks reviewer for a verdict
// on task + changes + the command evidence. It never errors: a failed or
// unparseable verdict is a "takeover" with Parsed=false.
func ReviewChange(ctx context.Context, reviewer llm.Provider, task, changes string, verifyCommands []string, dir string) ReviewOutcome {
	cfg := &DraftVerifyConfig{Enabled: true, VerifyCommands: verifyCommands, Verdict: reviewer}
	sieve := cfg.runSieve(ctx, dir)
	v, ok, usage := cfg.requestVerdict(ctx, task, "", changes, sieve)
	return ReviewOutcome{
		Decision:    strings.ToLower(v.Kind.String()),
		Instruction: v.Instruction,
		Parsed:      ok,
		Usage:       usage,
		sieve:       sieve,
	}
}

// ReviseInstruction is the follow-up prompt for the worker after "revise".
func (o ReviewOutcome) ReviseInstruction() string {
	return reviseInstruction(o.Instruction, o.sieve)
}

// TakeoverEvidence renders the reviewer findings for the model taking over.
func (o ReviewOutcome) TakeoverEvidence() string {
	var b strings.Builder
	if strings.TrimSpace(o.Instruction) != "" {
		b.WriteString("Reviewer findings: " + strings.TrimSpace(o.Instruction) + "\n\n")
	}
	if o.SieveRed() {
		fmt.Fprintf(&b, "Verify command `%s` exited %d:\n%s\n\n", o.sieve.Command, o.sieve.Exit, o.sieve.Output)
	}
	return b.String()
}
