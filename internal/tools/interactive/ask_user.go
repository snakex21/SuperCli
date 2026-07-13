package interactive

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
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
	// Image is an optional project-relative path to a generated preview. The
	// presentation layer decides whether it can render it (WebGUI) or should
	// show the path as a fallback (TUI).
	Image string `json:"image,omitempty"`
	// ImagePrompt is the ready-to-copy generation prompt shown when no image
	// provider is configured, or alongside a preview for reproducibility.
	ImagePrompt string `json:"image_prompt,omitempty"`
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
	// AllowCustom exposes a free-form answer in addition to the suggested
	// options. SuperCli enables it for model-originated questions by default.
	AllowCustom bool `json:"allowCustom"`
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
	// Custom is the user's own answer when none of the suggested labels is a
	// good fit. It may accompany Selected in multi-select mode.
	Custom string `json:"custom,omitempty"`
}

type askQuestionParams struct {
	Question    string      `json:"question"`
	Header      string      `json:"header"`
	Options     []AskOption `json:"options"`
	MultiSelect bool        `json:"multiSelect"`
}

// askParams accepts the original one-question shape and a forms-style
// questions array. Multiple questions are presented sequentially by simple
// clients and can be rendered as one form by richer clients later without
// changing the model contract.
type askParams struct {
	Question    string              `json:"question"`
	Header      string              `json:"header"`
	Options     []AskOption         `json:"options"`
	MultiSelect bool                `json:"multiSelect"`
	Questions   []askQuestionParams `json:"questions,omitempty"`
}

// Validate keeps the original single-question validation contract used by
// embedders and tests. Execute additionally validates the questions form.
func (p askParams) Validate() error {
	return (askQuestionParams{Question: p.Question, Header: p.Header, Options: p.Options, MultiSelect: p.MultiSelect}).Validate()
}

// Validate returns nil if the parameters can be rendered. The
// tool refuses to push a malformed question to the user.
func (p askQuestionParams) Validate() error {
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
		Description: "Ask focused questions with 2-4 choices and a custom-answer fallback instead of guessing. Prefer 1-3; use up to 8 only when one coherent decision genuinely needs it. Default to text only. Add visual fields only when requested or materially useful; use 2-3 focused variants, never whole pages speculatively. image is a project-relative preview path; without a generator use preview plus optional image_prompt.",
		Schema:      `{"type":"object","properties":{"question":{"type":"string"},"header":{"type":"string","maxLength":12},"options":{"type":"array","minItems":2,"maxItems":4,"items":{"type":"object","properties":{"label":{"type":"string"},"description":{"type":"string"},"preview":{"type":"string"},"image":{"type":"string"},"image_prompt":{"type":"string"}},"required":["label"]}},"multiSelect":{"type":"boolean"},"questions":{"type":"array","minItems":1,"maxItems":8,"items":{"type":"object","properties":{"question":{"type":"string"},"header":{"type":"string","maxLength":12},"options":{"type":"array","minItems":2,"maxItems":4,"items":{"type":"object","properties":{"label":{"type":"string"},"description":{"type":"string"},"preview":{"type":"string"},"image":{"type":"string"},"image_prompt":{"type":"string"}},"required":["label"]}},"multiSelect":{"type":"boolean"}},"required":["question","options"]}}},"anyOf":[{"required":["question","options"]},{"required":["questions"]}]}`,
		Fn:          a.Execute,
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
	questions := p.Questions
	if len(questions) == 0 {
		questions = []askQuestionParams{{Question: p.Question, Header: p.Header, Options: p.Options, MultiSelect: p.MultiSelect}}
	}
	if len(questions) > 8 {
		err := fmt.Errorf("ask_user: questions count is %d, max 8", len(questions))
		return Result{Err: err}, err
	}
	for _, question := range questions {
		if err := question.Validate(); err != nil {
			return Result{Err: err}, err
		}
	}

	timeout := a.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	answers := make([]string, 0, len(questions))
	for i, question := range questions {
		respond := make(chan AskAnswer, 1)
		req := AskRequest{
			ID: newAskID(), Question: question.Question, Header: question.Header,
			Options: question.Options, MultiSelect: question.MultiSelect,
			AllowCustom: true, Respond: respond,
		}
		select {
		case a.In <- req:
		case <-ctx.Done():
			return Result{Err: fmt.Errorf("ask_user: %w", ctx.Err())}, ctx.Err()
		}
		timer := time.NewTimer(timeout)
		select {
		case ans := <-respond:
			timer.Stop()
			if ans.Cancelled {
				return Result{Text: fmt.Sprintf("question %d: user cancelled", i+1)}, nil
			}
			answers = append(answers, fmt.Sprintf("question %d (%s): %s", i+1, question.Question, formatAskAnswer(ans)))
		case <-ctx.Done():
			timer.Stop()
			return Result{Err: fmt.Errorf("ask_user: %w", ctx.Err())}, ctx.Err()
		case <-timer.C:
			err := fmt.Errorf("ask_user: user did not answer question %d within %v", i+1, timeout)
			return Result{Err: err}, err
		}
	}
	if len(answers) == 1 {
		// Preserve the original one-question tool output to avoid needless
		// prompt churn and keep old replay fixtures byte-compatible.
		return Result{Text: strings.TrimPrefix(answers[0], fmt.Sprintf("question 1 (%s): ", questions[0].Question))}, nil
	}
	return Result{Text: joinAnswers(answers)}, nil
}

// formatAskAnswer returns a human-readable summary the model
// sees as the tool output. The labels match what the user
// picked, in order; "user cancelled" is reported explicitly.
func formatAskAnswer(ans AskAnswer) string {
	if ans.Cancelled {
		return "user cancelled the question"
	}
	if len(ans.Selected) == 0 && ans.Custom != "" {
		return "user answered: " + ans.Custom
	}
	if len(ans.Selected) == 0 {
		return "user did not pick any option"
	}
	if ans.Custom != "" {
		return "user selected: " + joinLabels(ans.Selected) + "; custom note: " + ans.Custom
	}
	if ans.MultiSelect {
		return "user selected: " + joinLabels(ans.Selected)
	}
	if len(ans.Selected) == 1 {
		return "user selected: " + ans.Selected[0]
	}
	return "user selected: " + joinLabels(ans.Selected) + " (unexpected multiple for single-select)"
}

func joinAnswers(answers []string) string { return joinWith(answers, "\n") }

func joinWith(items []string, sep string) string {
	var out string
	for i, item := range items {
		if i > 0 {
			out += sep
		}
		out += item
	}
	return out
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
var askIDCounter atomic.Uint64

func newAskID() string {
	return fmt.Sprintf("ask-%d", askIDCounter.Add(1))
}
