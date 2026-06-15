package interactive

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// AskUser is the tool that lets the model ask the user a
// structured question with 2-4 options instead of guessing.
//
// Architecture: the tool is constructed with an input channel
// that receives AskRequest values. The TUI's event loop drains
// the channel, renders the question, and blocks for user input.
// The TUI then sends an AskAnswer back through the Respond
// channel embedded in the request.
//
// This keeps the model goroutine blocked on a single channel
// pair instead of coupling to Bubble Tea internals.
type AskUser struct {
	// In is where Execute pushes AskRequest values. Buffer >= 1
	// so the model goroutine never blocks waiting for the TUI to
	// start reading.
	In chan<- AskRequest

	// Timeout caps how long Execute waits for a user answer.
	// Zero means use the default (60s). When the timeout fires,
	// Execute returns an error with a "user did not answer" note.
	Timeout time.Duration
}

// NewAskUser returns an AskUser wired to in. Timeout defaults
// to 60s when zero is supplied.
func NewAskUser(in chan<- AskRequest) *AskUser {
	return &AskUser{In: in, Timeout: 60 * time.Second}
}

// AskOption is one choice offered to the user.
type AskOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Preview     string `json:"preview,omitempty"`
}

// AskRequest is sent from the tool to the TUI.
type AskRequest struct {
	// ID is a per-request unique id (uuid-like). The TUI may
	// surface it for debugging but does not act on it.
	ID string `json:"id"`
	// Question is the full question the model wants answered.
	Question string `json:"question"`
	// Header is a short tab/header label (max 12 chars).
	Header string `json:"header"`
	// Options is the list of choices (2..4).
	Options []AskOption `json:"options"`
	// MultiSelect lets the user pick several options. When
	// false, the user picks exactly one or cancels.
	MultiSelect bool `json:"multiSelect"`
	// Respond is the channel the TUI sends the answer to. The
	// tool's goroutine is blocked reading from this channel;
	// the TUI is the only writer.
	Respond chan AskAnswer `json:"-"`
}

// AskAnswer is what the TUI sends back. Cancelled takes
// precedence over Selected when true.
type AskAnswer struct {
	// Selected holds the labels chosen by the user, in the
	// order they were picked. Empty when Cancelled.
	Selected []string `json:"selected"`
	// MultiSelect is mirrored back so the model can confirm
	// the question type.
	MultiSelect bool `json:"multiSelect"`
	// Cancelled is true when the user pressed Esc.
	Cancelled bool `json:"cancelled"`
}

// askParams is what the model sends in the tool arguments.
type askParams struct {
	Question    string      `json:"question"`
	Header      string      `json:"header"`
	Options     []AskOption `json:"options"`
	MultiSelect bool        `json:"multiSelect"`
}

// Validate returns nil if the parameters can be rendered. The
// tool refuses to push a malformed question to the user.
func (p askParams) Validate() error {
	if p.Question == "" {
		return fmt.Errorf("ask_user: question is required")
	}
	if p.Header != "" && len(p.Header) > 12 {
		return fmt.Errorf("ask_user: header %q is %d chars, max 12", p.Header, len(p.Header))
	}
	if n := len(p.Options); n < 2 || n > 4 {
		return fmt.Errorf("ask_user: options count is %d, must be 2..4", n)
	}
	for i, opt := range p.Options {
		if opt.Label == "" {
			return fmt.Errorf("ask_user: option %d has empty label", i)
		}
	}
	return nil
}

// Spec returns the Tool descriptor registered in the registry.
func (a *AskUser) Spec() Tool {
	return Tool{
		Name:        "ask_user",
		Description: "Ask the user a question with 2-4 options instead of guessing. Use this when the user's intent is ambiguous or there are several reasonable approaches. The model will receive the chosen label(s) back and continue from there. Saves tokens by preventing the model from going down the wrong path.",
		Schema: `{
			"type": "object",
			"properties": {
				"question": {
					"type": "string",
					"description": "The question to ask the user (one short sentence)."
				},
				"header": {
					"type": "string",
					"maxLength": 12,
					"description": "Very short label (max 12 chars) shown above the options."
				},
				"options": {
					"type": "array",
					"minItems": 2,
					"maxItems": 4,
					"items": {
						"type": "object",
						"properties": {
							"label":       {"type": "string", "description": "1-5 word option label."},
							"description": {"type": "string", "description": "What this option means."},
							"preview":     {"type": "string", "description": "Optional preview of the result."}
						},
						"required": ["label"]
					}
				},
				"multiSelect": {
					"type": "boolean",
					"default": false,
					"description": "Whether the user may select multiple options."
				}
			},
			"required": ["question", "options"]
		}`,
		Fn: a.Execute,
	}
}

// Execute parses args, pushes an AskRequest on In, and blocks
// for the matching AskAnswer. Context cancellation and Timeout
// both abort the wait and return a tool error.
func (a *AskUser) Execute(ctx context.Context, args json.RawMessage) (Result, error) {
	var p askParams
	if err := json.Unmarshal(args, &p); err != nil {
		return Result{Err: fmt.Errorf("ask_user: bad args: %w", err)}, err
	}
	if err := p.Validate(); err != nil {
		return Result{Err: err}, err
	}

	timeout := a.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	respond := make(chan AskAnswer, 1)
	req := AskRequest{
		ID:          newAskID(),
		Question:    p.Question,
		Header:      p.Header,
		Options:     p.Options,
		MultiSelect: p.MultiSelect,
		Respond:     respond,
	}

	// Push the request; respect context cancel.
	select {
	case a.In <- req:
	case <-ctx.Done():
		return Result{Err: fmt.Errorf("ask_user: %w", ctx.Err())}, ctx.Err()
	}

	// Wait for the answer with timeout.
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case ans := <-respond:
		return Result{Text: formatAskAnswer(ans)}, nil
	case <-ctx.Done():
		return Result{Err: fmt.Errorf("ask_user: %w", ctx.Err())}, ctx.Err()
	case <-timer.C:
		err := fmt.Errorf("ask_user: user did not answer within %v", timeout)
		return Result{Err: err}, err
	}
}

// formatAskAnswer returns a human-readable summary the model
// sees as the tool output. The labels match what the user
// picked, in order; "user cancelled" is reported explicitly.
func formatAskAnswer(ans AskAnswer) string {
	if ans.Cancelled {
		return "user cancelled the question"
	}
	if len(ans.Selected) == 0 {
		return "user did not pick any option"
	}
	if ans.MultiSelect {
		return "user selected: " + joinLabels(ans.Selected)
	}
	if len(ans.Selected) == 1 {
		return "user selected: " + ans.Selected[0]
	}
	return "user selected: " + joinLabels(ans.Selected) + " (unexpected multiple for single-select)"
}

func joinLabels(ls []string) string {
	out := ""
	for i, l := range ls {
		if i > 0 {
			out += ", "
		}
		out += l
	}
	return out
}

// newAskID returns a short pseudo-unique id for log correlation.
// F4 will replace this with a real uuid library; for F2 the
// monotonic counter is enough.
var askIDCounter uint64

func newAskID() string {
	askIDCounter++
	return fmt.Sprintf("ask-%d", askIDCounter)
}
