package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"supercli/internal/llm"
	"supercli/internal/llm/consult"
)

// Consult is the F12 opt-in tool. The model calls
// it when it wants a second opinion: N independent
// one-shot answers from cheap/free providers, with
// the running main provider as judge and tiebreaker.
//
// Schema:
//
//	{
//	  "question": string (required) — passed verbatim to
//	              each sample and to the judge
//	  "n":        int    (optional) — number of parallel
//	              samples; default 3, max = configured
//	              len(c.Council.Samples)
//	}
//
// Result text the model sees is the WINNER's
// response plus a one-line "judge said <reason>"
// footer. Other candidates' tokens do NOT enter
// the model's context — they are rendered to the
// TUI transcript (and recorded as a ConsultEvent)
// but stay out of the loop. This is the F2
// token-economy win for F12: parallel sampling
// without polluting the main conversation.
type Consult struct {
	// Council is the consult engine. nil disables
	// the tool (it returns a friendly text rather
	// than erroring, so the model can recover).
	Council *consult.Council

	// DefaultN is used when the model omits n.
	// 0 means 3.
	DefaultN int

	// MaxN caps the model's request. 0 means
	// len(c.Council.Samples) (capped silently).
	MaxN int

	// OnResult is called with the full consult
	// result AFTER the tool returns. main.go uses
	// it to emit a ConsultEvent for the TUI.
	// nil = no callback.
	OnResult func(consult.Result)

	// BuildProvider builds a provider for an explicit
	// model spec ("providerName/modelID" or a bare
	// model id). It powers the optional "models"
	// parameter, letting the agent consult SPECIFIC
	// configured models (local or online) instead of
	// the auto-picked cheapest pool. nil disables
	// the parameter.
	BuildProvider func(spec string) (llm.Provider, error)
}

// NewConsult returns a Consult bound to c.
// Default N 3, MaxN falls back to len(c.Samples).
func NewConsult(c *consult.Council) *Consult {
	return &Consult{Council: c, DefaultN: 3}
}

func (c *Consult) Spec() Tool {
	return Tool{
		Name: "consult",
		Description: "Ask N different free/cheap models the same question in parallel, then have the running main model judge the answers and pick the best. " +
			"Use this when a problem is ambiguous or has many defensible answers and you want a second opinion from independent voices. " +
			"Each sample is a one-shot answer (no tools, no memory) so this is cheap. The judge is the model you're already running, so no extra spend. " +
			"The model only sees the winner's response + a one-line judge reason; the full transcript is in the chat scrollback. " +
			"n defaults to 3 and is clamped to the configured sample pool. n=1 is allowed and skips the judge. " +
			"To consult SPECIFIC configured models instead of the auto-picked pool, pass `models` with explicit entries like \"providerName/modelID\" (or a bare model id); failures of individual models are reported per model without aborting the rest.",
		Schema: `{
			"type": "object",
			"properties": {
				"question": {"type": "string", "description": "The question to ask all samples (required)"},
				"n":        {"type": "integer", "description": "Number of parallel samples (default 3, min 1); ignored when models is given"},
				"models":   {"type": "array", "items": {"type": "string"}, "description": "Explicit models to consult, each \"providerName/modelID\" or a bare model id. Overrides the auto-picked pool."}
			},
			"required": ["question"]
		}`,
		Fn: c.run,
	}
}

type consultArgs struct {
	Question string   `json:"question"`
	N        int      `json:"n,omitempty"`
	Models   []string `json:"models,omitempty"`
}

func (c *Consult) run(ctx context.Context, args json.RawMessage) (Result, error) {
	if c.Council == nil {
		return Result{Text: "consult: not wired"}, nil
	}
	var a consultArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("consult: bad args: %w", err)}, nil
	}
	a.Question = strings.TrimSpace(a.Question)
	if a.Question == "" {
		return Result{Err: fmt.Errorf("consult: question is empty")}, nil
	}
	if len(a.Models) > 0 {
		return c.runSelected(ctx, a)
	}
	n := a.N
	if n <= 0 {
		n = c.DefaultN
		if n <= 0 {
			n = 3
		}
	}
	max := c.MaxN
	if max <= 0 {
		max = len(c.Council.Samples)
	}
	if n > max {
		n = max
	}
	res, err := c.Council.Consult(ctx, consult.Request{
		Question: a.Question,
		N:        n,
	})
	if err != nil {
		return Result{Err: fmt.Errorf("consult: %w", err)}, nil
	}
	if c.OnResult != nil {
		c.OnResult(res)
	}
	// Format the response the model sees.
	if res.AllFailed {
		return Result{Text: "consult: all sample providers failed; no answer to return"}, nil
	}
	w := res.Candidates[res.Verdict.WinnerIndex]
	var b strings.Builder
	fmt.Fprintf(&b, "Winner (#%d, provider=%s):\n", res.Verdict.WinnerIndex+1, w.Provider)
	b.WriteString(w.Response)
	b.WriteString("\n\n")
	if res.Verdict.Reason != "" {
		fmt.Fprintf(&b, "Judge: %s\n", res.Verdict.Reason)
	}
	fmt.Fprintf(&b, "\n[consult: %d candidate(s), %d total tokens]", len(res.Candidates), res.TotalTokens)
	return Result{Text: b.String()}, nil
}

// runSelected handles the explicit `models` parameter:
// build a provider per spec, fan out over exactly those,
// and report per-model status. Single-model failures
// (build or call) never abort the rest.
func (c *Consult) runSelected(ctx context.Context, a consultArgs) (Result, error) {
	if c.BuildProvider == nil {
		return Result{Text: "consult: explicit model selection is not wired; call without `models`"}, nil
	}
	var provs []llm.Provider
	var specs []string
	var buildErrs []string
	for _, s := range a.Models {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		p, err := c.BuildProvider(s)
		if err != nil {
			buildErrs = append(buildErrs, fmt.Sprintf("model %s: error: %v", s, err))
			continue
		}
		provs = append(provs, p)
		specs = append(specs, s)
	}
	if len(provs) == 0 {
		return Result{Err: fmt.Errorf("consult: no usable models in %v: %s", a.Models, strings.Join(buildErrs, "; "))}, nil
	}
	res, err := c.Council.ConsultSelected(ctx, a.Question, provs)
	if err != nil {
		return Result{Err: fmt.Errorf("consult: %w", err)}, nil
	}
	if c.OnResult != nil {
		c.OnResult(res)
	}
	var b strings.Builder
	if res.AllFailed {
		b.WriteString("consult: every selected model failed\n")
	} else if w := res.Verdict.WinnerIndex; w >= 0 && w < len(res.Candidates) {
		fmt.Fprintf(&b, "Winner (%s):\n%s\n\n", specs[w], res.Candidates[w].Response)
		if res.Verdict.Reason != "" {
			fmt.Fprintf(&b, "Judge: %s\n", res.Verdict.Reason)
		}
	}
	b.WriteString("\nPer-model status:\n")
	for i, cd := range res.Candidates {
		if cd.Err != nil {
			fmt.Fprintf(&b, "- model %s: error: %v\n", specs[i], cd.Err)
		} else {
			fmt.Fprintf(&b, "- model %s: ok (%s, %d tok)\n", specs[i], cd.Elapsed.Round(time.Millisecond), cd.Total)
		}
	}
	for _, e := range buildErrs {
		b.WriteString("- " + e + "\n")
	}
	fmt.Fprintf(&b, "[consult: %d model(s), %d total tokens]", len(res.Candidates), res.TotalTokens)
	return Result{Text: b.String()}, nil
}
