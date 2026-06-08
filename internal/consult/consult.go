// Package consult implements F12: cross-model
// consultation. The model calls Consult to get N
// independent one-shot answers from cheap/free
// providers in parallel, with the running main
// provider acting as judge to pick the best.
//
// The "council" metaphor is intentional: N
// independent voices, one arbitrator. The package
// is split from darwin because the semantics
// differ:
//
//	darwin  — N parallel runs of the AGENT itself
//	          on a prompt, judge picks best
//	consult — N different cheap MODELS answering
//	          once, judge picks best
//
// Both share a tolerant LLM judge (code-fence +
// prose-tolerant JSON parser) so they fail
// gracefully on marginal outputs.
package consult

import (
	"context"
	"fmt"
	"sync"
	"time"

	"supercli/internal/llm"
)

// Request is the input to a single consultation.
type Request struct {
	// Question is the user-style prompt. It is
	// passed verbatim to each sample provider
	// AND to the judge. Empty questions are
	// rejected up front.
	Question string

	// N is the desired number of parallel
	// samples. n <= 0 means "use all
	// configured samples in c.Samples".
	// n greater than len(c.Samples) is clamped
	// silently. n=1 collapses to a no-judge
	// pass-through (the sole sample IS the
	// answer).
	N int
}

// Candidate is one sample's response. Err is
// non-nil when the provider failed (network,
// rate-limit, parse); in that case Response is
// empty and In/Out/Total are 0.
type Candidate struct {
	Index    int
	Provider string
	Response string
	In       int64
	Out      int64
	Total    int64
	Err      error
}

// Verdict is the judge's pick. WinnerIndex is
// 0-based into the post-filter slice of
// successful candidates; Reason is one short
// sentence. JudgeIn/JudgeOut are the judge's own
// token counts (F7 budget impact).
type Verdict struct {
	WinnerIndex int
	Reason      string
	JudgeIn     int64
	JudgeOut    int64
}

// Result is what Consult returns. The slice of
// candidates contains the SUCCESSFUL ones in
// call-order; failed candidates are NOT
// included. TotalTokens sums samples + judge.
type Result struct {
	Question    string
	Candidates  []Candidate
	Verdict     Verdict
	TotalTokens int64
	// AllFailed is set when every sample errored
	// AND we have nothing for the judge. The
	// caller (the tool layer) converts this to
	// a user-visible error.
	AllFailed bool
}

// Council is the consult engine. It is built
// once at startup and shared across the loop.
type Council struct {
	// Samples is the ordered list of cheap/free
	// providers to fan out across. The order
	// matters for determinism: samples[0] is
	// always called first, samples[N-1] last.
	// Length is the maximum N that can be
	// requested.
	Samples []llm.Provider

	// Judge is the running main provider. It
	// reads the question + all candidate
	// responses and returns a JSON verdict.
	// Judge MUST be non-nil for Consult to
	// succeed; n=1 bypasses it.
	Judge llm.Provider

	// MaxTokens caps the judge call. 0 means
	// 300. F12 keeps the judge tiny so the
	// main loop's context doesn't blow up.
	MaxTokens int

	// PerCandidateCap is the max chars kept
	// from a sample's response when fed to
	// the judge. 0 means 4000. F12 prefers
	// short, opinionated answers; long rants
	// waste judge context.
	PerCandidateCap int

	// Logger is optional. When set it receives
	// a single line per Consult call.
	Logger func(format string, args ...any)

	// NowFn is optional, for test freezing.
	// nil means time.Now.
	NowFn func() time.Time
}

// Consult runs a single council. The flow:
//
//  1. Resolve N providers from c.Samples
//  2. Fan out N goroutines, each calls
//     provider.Complete(ctx, [user(question)])
//  3. Wait for all, collect deltas
//  4. If 0 succeeded → return AllFailed=true
//  5. If only 1 succeeded AND N requested > 1 →
//     skip the judge and return that one as
//     the winner (the judge would be
//     meaningless with no choice)
//  6. Else call the judge, parse verdict,
//     return Result
//
// ctx cancellation is honoured by every
// goroutine: the streaming provider is expected
// to stop on ctx.Done and we read the channel
// drain.
func (c *Council) Consult(ctx context.Context, req Request) (Result, error) {
	if req.Question == "" {
		return Result{}, fmt.Errorf("consult: empty question")
	}
	if c.Judge == nil {
		return Result{}, fmt.Errorf("consult: no judge")
	}
	if len(c.Samples) == 0 {
		return Result{}, fmt.Errorf("consult: no sample providers")
	}
	// Resolve N. n <= 0 means "all".
	n := req.N
	if n <= 0 || n > len(c.Samples) {
		n = len(c.Samples)
	}
	providers := c.Samples[:n]
	if c.Logger != nil {
		c.Logger("consult: %d sample(s), %d candidate(s) expected", n, n)
	}
	// Fan out.
	cands := c.fanOut(ctx, req.Question, providers)
	// Filter failures. Preserve original index
	// (1-based) so the judge prompt reads
	// "Candidate 1, Candidate 2..." naturally.
	good := make([]Candidate, 0, len(cands))
	for i, cd := range cands {
		if cd.Err == nil {
			cd.Index = i + 1 // 1-based
			good = append(good, cd)
		}
	}
	if len(good) == 0 {
		return Result{
			Question:   req.Question,
			Candidates: nil,
			AllFailed:  true,
		}, nil
	}
	// Total tokens so far (samples only).
	var total int64
	for _, cd := range good {
		total += cd.In + cd.Out
	}
	// Single-candidate shortcut.
	if len(good) == 1 {
		if c.Logger != nil {
			c.Logger("consult: only 1 candidate, skipping judge")
		}
		return Result{
			Question: req.Question,
			Candidates: good,
			Verdict: Verdict{
				WinnerIndex: 0,
				Reason:      "only one candidate succeeded",
			},
			TotalTokens: total,
		}, nil
	}
	// Judge.
	cap := c.PerCandidateCap
	if cap <= 0 {
		cap = 4000
	}
	maxTok := c.MaxTokens
	if maxTok <= 0 {
		maxTok = 300
	}
	verdict, err := c.judge(ctx, req.Question, good, cap, maxTok)
	if err != nil {
		// Judge failed: surface the candidates
		// without a verdict. The tool layer can
		// then return "consult: judge failed"
		// plus the raw text, and the model can
		// still pick.
		if c.Logger != nil {
			c.Logger("consult: judge failed: %v", err)
		}
		return Result{
			Question:    req.Question,
			Candidates:  good,
			Verdict:     Verdict{Reason: fmt.Sprintf("judge failed: %v", err)},
			TotalTokens: total,
		}, nil
	}
	total += verdict.JudgeIn + verdict.JudgeOut
	return Result{
		Question:    req.Question,
		Candidates:  good,
		Verdict:     verdict,
		TotalTokens: total,
	}, nil
}

// fanOut runs all sample providers concurrently
// and returns one Candidate per call, preserving
// order. Each Candidate's Index is 0-based at
// this point; Consult sets the 1-based index
// after filtering.
func (c *Council) fanOut(ctx context.Context, q string, ps []llm.Provider) []Candidate {
	out := make([]Candidate, len(ps))
	var wg sync.WaitGroup
	wg.Add(len(ps))
	for i, p := range ps {
		i, p := i, p
		go func() {
			defer wg.Done()
			out[i] = c.callOne(ctx, p, q)
		}()
	}
	wg.Wait()
	return out
}

// callOne invokes a single sample provider and
// returns its aggregated Candidate. The provider
// interface is streaming; we collect deltas into
// a buffer and capture the final Usage.
func (c *Council) callOne(ctx context.Context, p llm.Provider, q string) Candidate {
	if p == nil {
		return Candidate{Provider: "<nil>", Err: fmt.Errorf("nil provider")}
	}
	name := p.Name()
	stream, err := p.Complete(ctx, []llm.Message{{
		Role:    llm.RoleUser,
		Content: q,
	}}, nil) // no tools for consultation
	if err != nil {
		return Candidate{Provider: name, Err: fmt.Errorf("provider init: %w", err)}
	}
	var buf []byte
	var usage *llm.Usage
	for d := range stream {
		if d.Err != nil {
			return Candidate{Provider: name, Err: fmt.Errorf("stream: %w", d.Err)}
		}
		if d.Content != "" {
			buf = append(buf, d.Content...)
		}
		if d.Usage != nil {
			usage = d.Usage
		}
	}
	// Token accounting. Total = Input + Output
	// if Total is missing, derive it.
	var in, out, tot int64
	if usage != nil {
		in, out, tot = int64(usage.Input), int64(usage.Output), int64(usage.Total)
	}
	if tot == 0 {
		tot = in + out
	}
	return Candidate{
		Provider: name,
		Response: string(buf),
		In:       in,
		Out:      out,
		Total:    tot,
	}
}

// judge builds the judge prompt, calls the main
// provider, and parses the verdict.
func (c *Council) judge(ctx context.Context, q string, cands []Candidate, cap, maxTok int) (Verdict, error) {
	sys := "You are a strict judge evaluating parallel answers from different models. " +
		"Pick the single best one. Reply ONLY with JSON: " +
		"{\"winner\": <1-based int>, \"reason\": <one short sentence>}. No prose, no markdown."
	user := renderJudgePrompt(q, cands, cap)
	stream, err := c.Judge.Complete(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: sys},
		{Role: llm.RoleUser, Content: user},
	}, nil) // judge doesn't need tools
	if err != nil {
		return Verdict{}, fmt.Errorf("provider init: %w", err)
	}
	var buf []byte
	var usage *llm.Usage
	for d := range stream {
		if d.Err != nil {
			return Verdict{}, fmt.Errorf("stream: %w", d.Err)
		}
		if d.Content != "" {
			buf = append(buf, d.Content...)
		}
		if d.Usage != nil {
			usage = d.Usage
		}
	}
	v, err := parseJudgeVerdict(string(buf), len(cands))
	if err != nil {
		return Verdict{}, err
	}
	if usage != nil {
		v.JudgeIn = int64(usage.Input)
		v.JudgeOut = int64(usage.Output)
	}
	return v, nil
}
