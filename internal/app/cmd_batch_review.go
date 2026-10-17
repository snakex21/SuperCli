package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/llm/factory"
	"supercli/internal/system/config"
)

// Post-hoc review for batch runs (config `review_model`): the main (cheap)
// model does the task, then a stronger model judges the resulting changes in
// one tool-free call. revise sends its instruction back to the main model;
// takeover (or a time handoff) lets the reviewer finish the task itself in a
// fresh context that starts from the reviewer's findings.

// batchRunResult is the accounting of one or more loop runs.
type batchRunResult struct {
	Usage agent.Usage
	Steps int
	Model string
	Err   error
}

func (r *batchRunResult) add(o batchRunResult) {
	r.Usage = addAgentUsage(r.Usage, o.Usage)
	r.Steps += o.Steps
	if o.Model != "" {
		r.Model = o.Model
	}
	r.Err = o.Err
}

func addAgentUsage(a, b agent.Usage) agent.Usage {
	a.Input += b.Input
	a.Output += b.Output
	a.Total += b.Total
	a.Cached += b.Cached
	a.CacheWrite += b.CacheWrite
	a.Reasoning += b.Reasoning
	return a
}

// batchTraceReview is the `review` object of the final trace usage line:
// where the tokens went (worker = main model, review + takeover = reviewer).
type batchTraceReview struct {
	Decisions      []string `json:"decisions"`
	Instructions   []string `json:"instructions,omitempty"`
	ChangesBytes   []int    `json:"changesBytes,omitempty"`
	SieveRed       []bool   `json:"sieveRed,omitempty"`
	SieveOutput    []string `json:"sieveOutput,omitempty"`
	Handoff        bool     `json:"handoff,omitempty"`
	Takeover       bool     `json:"takeover,omitempty"`
	WorkerInput    int      `json:"workerInput"`
	WorkerOutput   int      `json:"workerOutput"`
	WorkerSteps    int      `json:"workerSteps"`
	ReviewInput    int      `json:"reviewInput"`
	ReviewOutput   int      `json:"reviewOutput"`
	ReviewCached   int      `json:"reviewCached"`
	TakeoverInput  int      `json:"takeoverInput,omitempty"`
	TakeoverOutput int      `json:"takeoverOutput,omitempty"`
	TakeoverCached int      `json:"takeoverCached,omitempty"`
	TakeoverSteps  int      `json:"takeoverSteps,omitempty"`
}

type batchReviewer struct {
	provider        llm.Provider
	reasoningEffort string
	verify          []string
	maxRounds       int
	takeover        bool
	handoffAfter    time.Duration
	home            string
	before          map[string]string
	timer           *time.Timer
	fired           atomic.Bool
	trace           batchTraceReview
}

// newBatchReviewer returns nil when review_model is unset or cannot be built.
func newBatchReviewer(tomlCfg config.TomlConfig, cfg config.Config, provFactory *factory.Factory, home string) *batchReviewer {
	reviewCfg, ok := resolveReviewConfig(tomlCfg, cfg)
	if !ok {
		return nil
	}
	p, err := provFactory.Build(reviewCfg, llm.PurposeMain)
	if err != nil {
		log.Printf("review_model: provider %q build failed: %v — review disabled", tomlCfg.ReviewModel, err)
		return nil
	}
	r := &batchReviewer{
		provider:        p,
		reasoningEffort: tomlCfg.ReviewReasoningEffort,
		verify:          tomlCfg.VerifyCommands,
		maxRounds:       tomlCfg.ReviewMaxRounds,
		takeover:        tomlCfg.ReviewTakeover != nil && *tomlCfg.ReviewTakeover,
		home:            home,
	}
	if r.maxRounds <= 0 {
		r.maxRounds = 1
	}
	if s := strings.TrimSpace(tomlCfg.ReviewHandoffAfter); s != "" {
		if d, err := time.ParseDuration(s); err == nil && d > 0 {
			r.handoffAfter = d
		} else {
			log.Printf("review_handoff_after: invalid duration %q — no time handoff", s)
		}
	}
	r.before = agent.SnapshotTree(home)
	fmt.Fprintf(os.Stderr, "[review] on · reviewer=%s · verify=%v · max_rounds=%d · takeover=%v · handoff_after=%s\n",
		p.Name(), r.verify, r.maxRounds, r.takeover, r.handoffAfter)
	return r
}

// workerContext bounds every main-model run (first pass and revisions) by the
// handoff deadline, measured from the start of the task.
func (r *batchReviewer) workerContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if r == nil || !r.takeover || r.handoffAfter <= 0 {
		return ctx, func() {}
	}
	wctx, cancel := context.WithCancel(ctx)
	r.timer = time.AfterFunc(r.handoffAfter, func() {
		r.fired.Store(true)
		fmt.Fprintf(os.Stderr, "[review] handoff: main model still working after %s\n", r.handoffAfter)
		cancel()
	})
	return wctx, func() { r.timer.Stop(); cancel() }
}

func (r *batchReviewer) handedOff() bool { return r != nil && r.fired.Load() }

// finish reviews the worker's result and runs revise / takeover passes.
// run executes one prompt on a loop and drains its events.
func (r *batchReviewer) finish(ctx, workerCtx context.Context, task string, l *agent.Loop, loopCfg agent.LoopConfig,
	first batchRunResult, run func(context.Context, *agent.Loop, string) batchRunResult) batchRunResult {
	total := first
	r.addWorker(first)
	reason, evidence := "", ""
	if r.handedOff() {
		reason = "handoff"
	} else if first.Err != nil {
		reason = "error"
	}
	for round := 0; reason == ""; round++ {
		changes := agent.RenderTreeChanges(r.home, r.before)
		reviewCtx := llm.WithIndependentReasoningEffort(ctx, r.reasoningEffort)
		out := agent.ReviewChange(reviewCtx, r.provider, task, changes, r.verify, r.home)
		r.trace.Decisions = append(r.trace.Decisions, out.Decision)
		r.trace.Instructions = append(r.trace.Instructions, out.Instruction)
		r.trace.ChangesBytes = append(r.trace.ChangesBytes, len(changes))
		r.trace.SieveRed = append(r.trace.SieveRed, out.SieveRed())
		r.trace.SieveOutput = append(r.trace.SieveOutput, out.SieveSummary())
		r.trace.ReviewInput += out.Usage.Input
		r.trace.ReviewOutput += out.Usage.Output
		r.trace.ReviewCached += out.Usage.CachedInput
		total.Usage = addAgentUsage(total.Usage, agent.Usage{Input: out.Usage.Input, Output: out.Usage.Output,
			Total: out.Usage.Input + out.Usage.Output, Cached: out.Usage.CachedInput})
		fmt.Fprintf(os.Stderr, "[review] round=%d decision=%s parsed=%v sieve_red=%v in=%d out=%d\n",
			round+1, out.Decision, out.Parsed, out.SieveRed(), out.Usage.Input, out.Usage.Output)
		if out.Decision == "accept" {
			return total
		}
		if out.Decision == "revise" && round < r.maxRounds && workerCtx.Err() == nil {
			res := run(workerCtx, l, out.ReviseInstruction())
			r.addWorker(res)
			total.add(res)
			if res.Err != nil {
				if r.handedOff() {
					reason = "handoff"
				} else {
					reason, evidence = "error", out.TakeoverEvidence()
				}
			}
			continue
		}
		reason, evidence = out.Decision, out.TakeoverEvidence()
		if r.handedOff() {
			r.trace.Handoff = true
		}
	}
	if reason == "handoff" {
		r.trace.Handoff = true
	}
	if !r.takeover {
		return total
	}
	if r.timer != nil {
		r.timer.Stop()
	}
	cfg := loopCfg
	cfg.Provider = r.provider
	cfg.Writer = nil
	tl, err := agent.NewLoop(cfg)
	if err != nil {
		log.Printf("review takeover: %v", err)
		return total
	}
	r.trace.Takeover = true
	fmt.Fprintf(os.Stderr, "[review] takeover by %s (reason=%s)\n", r.provider.Name(), reason)
	res := run(llm.WithIndependentReasoningEffort(ctx, r.reasoningEffort), tl, reviewTakeoverPrompt(task, reason, evidence, agent.RenderTreeChanges(r.home, r.before)))
	r.trace.TakeoverInput += res.Usage.Input
	r.trace.TakeoverOutput += res.Usage.Output
	r.trace.TakeoverCached += res.Usage.Cached
	r.trace.TakeoverSteps += res.Steps
	total.add(res)
	return total
}

func (r *batchReviewer) addWorker(res batchRunResult) {
	r.trace.WorkerInput += res.Usage.Input
	r.trace.WorkerOutput += res.Usage.Output
	r.trace.WorkerSteps += res.Steps
}

func reviewTakeoverPrompt(task, reason, evidence, changes string) string {
	why := "a reviewer rejected its result"
	if reason == "handoff" {
		why = "it ran out of time before finishing"
	}
	var b strings.Builder
	b.WriteString(strings.TrimSpace(task))
	b.WriteString("\n\n---\nA cheaper model already worked on this task and " + why + ". Its changes are in the working tree.\n\n")
	b.WriteString(evidence)
	if strings.TrimSpace(changes) != "" {
		b.WriteString("Current changes:\n" + changes + "\n\n")
	}
	b.WriteString("Finish the task correctly yourself: keep what is right, fix what is wrong, and run the tests before answering.")
	return b.String()
}

// The tree snapshot / change rendering now lives in package agent
// (agent.SnapshotTree / agent.RenderTreeChanges) so the interactive
// post-work review shares exactly the batch behaviour.
