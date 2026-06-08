package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"supercli/internal/consult"
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
			"n defaults to 3 and is clamped to the configured sample pool. n=1 is allowed and skips the judge.",
		Schema: `{
			"type": "object",
			"properties": {
				"question": {"type": "string", "description": "The question to ask all samples (required)"},
				"n":        {"type": "integer", "description": "Number of parallel samples (default 3, min 1)"}
			},
			"required": ["question"]
		}`,
		Fn: c.run,
	}
}

type consultArgs struct {
	Question string `json:"question"`
	N        int    `json:"n,omitempty"`
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
