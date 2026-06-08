// bridge.go runs the draft call. The bridge is a thin
// wrapper around an llm.Provider — it builds a
// "propose a brief plan" prompt, calls the draft
// model, and returns the result. The actual injection
// into the verifier's view is the loop's responsibility
// (Bridge doesn't know about the main Messages list).

package draft

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"supercli/internal/llm"
)

// DraftSystemPrompt is the instruction we give the
// draft model. Kept short so the draft model has a
// reason to be terse (and so we can use cheap models
// that don't follow long instructions well).
const DraftSystemPrompt = `You are a planning assistant. Given the user's request, produce a SHORT numbered plan (3-6 steps) the main model can use as a starting point.

Rules:
- Be terse. Each step is one line, no preamble.
- Cover the high-level approach, not the implementation.
- If the request is ambiguous, state the assumption in one line.
- Do NOT use tools. Just write the plan.
- Do NOT repeat the user's question.`

// DraftResult is the bridge's output for one call.
type DraftResult struct {
	// Text is the draft model's plan text. Trimmed
	// of leading/trailing whitespace.
	Text string

	// Tokens is the draft model's output token count
	// (best-effort; providers that don't report
	// usage return 0).
	Tokens int

	// Model is the draft model id actually used
	// (after the policy resolved any "" Model).
	Model string

	// Duration is the wall-clock time spent on the
	// draft call. The TUI marker does not show it
	// but it's useful in logs.
	Duration time.Duration
}

// Bridge runs draft calls. Construct with NewBridge
// (which takes a draft provider and a policy).
type Bridge struct {
	policy *Policy
	prov   llm.Provider
	// We do NOT take a credit tracker; draft calls
	// are recorded against credits by the loop after
	// the bridge returns (the loop already has the
	// tracker in scope).
}

// ModelName returns the draft provider's model id
// (e.g. "claude-haiku-4-5"). Used by the agent loop
// to record the draft's spend in the credit ledger
// and to label the DraftUsedEvent / override record.
func (b *Bridge) ModelName() string {
	if b == nil || b.prov == nil {
		return ""
	}
	return b.prov.Name()
}

// NewBridge returns a Bridge for the given policy and
// provider. The provider is the DRAFT provider (a
// separate llm.Provider with a different model id
// than the verifier's). Nil policy or nil provider
// is an error.
func NewBridge(p *Policy, prov llm.Provider) (*Bridge, error) {
	if p == nil {
		return nil, errors.New("draft.NewBridge: policy is nil")
	}
	if prov == nil {
		return nil, errors.New("draft.NewBridge: provider is nil")
	}
	return &Bridge{policy: p, prov: prov}, nil
}

// Plan calls the draft model with the given user
// prompt and returns the plan text. The provider is
// invoked via Complete; the result is the last
// assistant message in the delta stream.
//
// Errors:
//   - ctx.Err()        — context cancellation
//   - any error from Complete
//
// Empty Text in the result is possible (some models
// produce a blank plan) — the loop treats empty-text
// results as "no draft injected" and skips the
// event / system message.
func (b *Bridge) Plan(ctx context.Context, userPrompt string) (DraftResult, error) {
	if userPrompt == "" {
		return DraftResult{}, errors.New("draft.Bridge.Plan: userPrompt is empty")
	}
	start := time.Now()
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: DraftSystemPrompt},
		{Role: llm.RoleUser, Content: userPrompt},
	}
	ch, err := b.prov.Complete(ctx, msgs, nil) // drafts don't need tools
	if err != nil {
		return DraftResult{Model: b.prov.Name(), Duration: time.Since(start)}, err
	}
	var (
		text   strings.Builder
		tokens int
	)
	for d := range ch {
		if d.Err != nil {
			return DraftResult{Model: b.prov.Name(), Duration: time.Since(start)}, d.Err
		}
		if d.Content != "" {
			text.WriteString(d.Content)
		}
		if d.Usage != nil {
			tokens = d.Usage.Output
		}
	}
	out := strings.TrimSpace(text.String())
	return DraftResult{
		Text:     out,
		Tokens:   tokens,
		Model:    b.prov.Name(),
		Duration: time.Since(start),
	}, nil
}

// AsSystemMessage wraps the draft's plan in the
// marker the loop injects into the verifier's view.
// The bracketed prefix lets the verifier's response
// (and F5's reflector) recognize the message as a
// draft contribution for overlap analysis.
func (b *Bridge) AsSystemMessage(plan string) llm.Message {
	return llm.Message{
		Role:    llm.RoleSystem,
		Content: fmt.Sprintf("[draft plan] %s", plan),
	}
}
