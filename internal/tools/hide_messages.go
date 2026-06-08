package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// Hider is the F14 contract the hide_messages tool needs
// from the agent loop. It is satisfied by *agent.Loop
// (via HideRange) but lives here to keep the tool
// independent of the agent package.
type Hider interface {
	// HideRange marks messages [from, to) as hidden from
	// the provider's context view. Returns an error on
	// invalid range.
	HideRange(from, to int) error
}

// HideMessages is the F14 opt-in tool. The model calls
// it when it wants to drop old messages from the
// provider's context (visible in TUI, persisted, and
// searchable via F13, but stripped from the next
// provider call). Hidden messages are collapsed into a
// single "[earlier context cleared — N message(s)
// compacted]" placeholder.
//
// Schema:
//
//	{
//	  "from": int (required) — 0-based start index, inclusive
//	  "to":   int (required) — 0-based end index, exclusive
//	}
//
// The model should use `search_history` first to find the
// index range it wants to drop.
type HideMessages struct {
	// Hider is the loop or test stub. nil disables the tool.
	Hider Hider
	// LenFn returns the current l.Messages length. The
	// tool validates (from, to) against this. Required
	// when Hider is set; the tool fails closed if nil.
	LenFn func() int
}

// NewHideMessages returns a HideMessages bound to h.
// lenFn is required so the tool can validate the range
// (we don't want to leak an error to the model after the
// mutation).
func NewHideMessages(h Hider, lenFn func() int) *HideMessages {
	return &HideMessages{Hider: h, LenFn: lenFn}
}

// Spec returns the tools.Tool description for the registry.
func (h *HideMessages) Spec() Tool {
	return Tool{
		Name: "hide_messages",
		Description: "Hide a range of messages from the model's context view. " +
			"Hidden messages stay visible in the TUI, stay persisted to the session, " +
			"and stay searchable via search_history — only the next provider call " +
			"sees them replaced by a single placeholder. Use this to trim long " +
			"conversations when the model itself decides prior turns are no longer " +
			"needed for context. Auto budget eviction runs on top of this and is " +
			"independent. The (from, to) range is 0-based half-open; use " +
			"search_history first to find the indices.",
		Schema: `{
			"type": "object",
			"properties": {
				"from": {"type": "integer", "description": "0-based start index (inclusive)"},
				"to":   {"type": "integer", "description": "0-based end index (exclusive)"}
			},
			"required": ["from", "to"]
		}`,
		Fn: h.run,
	}
}

type hideMessagesArgs struct {
	From int `json:"from"`
	To   int `json:"to"`
}

func (h *HideMessages) run(ctx context.Context, args json.RawMessage) (Result, error) {
	if h.Hider == nil || h.LenFn == nil {
		return Result{Text: "hide_messages: not wired"}, nil
	}
	var a hideMessagesArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("hide_messages: bad args: %w", err)}, nil
	}
	if a.From < 0 || a.To < 0 {
		return Result{Err: fmt.Errorf("hide_messages: negative indices (from=%d to=%d)", a.From, a.To)}, nil
	}
	if a.From > a.To {
		return Result{Err: fmt.Errorf("hide_messages: from (%d) > to (%d)", a.From, a.To)}, nil
	}
	if a.From == a.To {
		return Result{Text: "hide_messages: empty range, nothing to do"}, nil
	}
	total := h.LenFn()
	if a.To > total {
		return Result{Err: fmt.Errorf("hide_messages: to=%d exceeds message count %d", a.To, total)}, nil
	}
	if err := h.Hider.HideRange(a.From, a.To); err != nil {
		return Result{Err: fmt.Errorf("hide_messages: %w", err)}, nil
	}
	return Result{
		Text: fmt.Sprintf("hide_messages: hid %d message(s) [%d, %d)", a.To-a.From, a.From, a.To),
	}, nil
}
