// Package windowautomation uses accessibility actions on one explicitly selected
// process window. It never injects desktop input or changes the foreground window.
package windowautomation

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

const MaxElements = 128

type Identity struct {
	PID     int    `json:"pid"`
	Created uint64 `json:"created"`
}

// Process pins the native process identity until Close; PID reuse cannot extend
// a grant to a newly started program. It does not own or terminate the process.
type Process struct {
	Identity
	Name   string
	handle uintptr
}

type Request struct {
	Identity
	Action      string `json:"action"`
	WindowTitle string `json:"window_title,omitempty"`
	WindowID    string `json:"window_id,omitempty"`
	ElementID   string `json:"element_id,omitempty"`
	Text        string `json:"text,omitempty"`
}

type Element struct {
	ID             string   `json:"id,omitempty"`
	Depth          int      `json:"depth"`
	Role           string   `json:"role"`
	Name           string   `json:"name,omitempty"`
	AutomationID   string   `json:"automation_id,omitempty"`
	Enabled        bool     `json:"enabled"`
	Password       bool     `json:"password,omitempty"`
	Value          string   `json:"value,omitempty"`
	ValueTruncated bool     `json:"value_truncated,omitempty"`
	Actions        []string `json:"actions,omitempty"`
	Expansion      string   `json:"expansion,omitempty"`
	// PopupMenu is the handle of the open popup menu window (class #32768)
	// this element belongs to; empty for elements of the selected window.
	PopupMenu string `json:"popup_menu,omitempty"`
}

// WindowCandidate is one top-level window of the target process considered
// when neither window_title nor window_id was given.
type WindowCandidate struct {
	HWND      uintptr
	Title     string
	Visible   bool
	Enabled   bool
	Owner     uintptr // GW_OWNER handle; 0 when unowned
	Menu      bool    // classic popup menu window (class #32768)
	Auxiliary bool    // tool/IME helper window
}

// PickWindow returns the index of the window an unqualified request targets,
// or -1 while the choice stays ambiguous. Popup menus are never targets. The
// rest is narrowed in order by preferring non-auxiliary, visible, enabled (a
// modal dialog disables its owner), owned by another candidate (dialogs are
// owned by their main window), then titled windows. Narrowing stops once one
// window remains; a filter that would leave no window is skipped.
func PickWindow(candidates []WindowCandidate) int {
	var set []int
	for i, c := range candidates {
		if !c.Menu {
			set = append(set, i)
		}
	}
	all := set
	ownedByCandidate := func(c WindowCandidate) bool {
		for _, i := range all {
			if c.Owner != 0 && c.Owner != c.HWND && candidates[i].HWND == c.Owner {
				return true
			}
		}
		return false
	}
	filters := []func(WindowCandidate) bool{
		func(c WindowCandidate) bool { return !c.Auxiliary },
		func(c WindowCandidate) bool { return c.Visible },
		func(c WindowCandidate) bool { return c.Enabled },
		ownedByCandidate,
		func(c WindowCandidate) bool { return c.Title != "" },
	}
	for _, keep := range filters {
		if len(set) <= 1 {
			break
		}
		var narrowed []int
		for _, i := range set {
			if keep(candidates[i]) {
				narrowed = append(narrowed, i)
			}
		}
		if len(narrowed) > 0 {
			set = narrowed
		}
	}
	if len(set) == 1 {
		return set[0]
	}
	return -1
}

// WindowChoices counts the selectable (non-menu) candidates and lists at most
// eight of them as `0xHWND "title"` for a window_id retry.
func WindowChoices(candidates []WindowCandidate) (int, string) {
	count := 0
	choices := make([]string, 0, 8)
	for _, c := range candidates {
		if c.Menu {
			continue
		}
		count++
		if len(choices) < 8 {
			title := []rune(c.Title)
			if len(title) > 120 {
				title = append(title[:117], '.', '.', '.')
			}
			choices = append(choices, fmt.Sprintf("0x%X %q", c.HWND, string(title)))
		}
	}
	list := strings.Join(choices, ", ")
	if count > len(choices) {
		list += fmt.Sprintf(", ... (%d more)", count-len(choices))
	}
	return count, list
}

func ambiguousWindowError(candidates []WindowCandidate) error {
	count, list := WindowChoices(candidates)
	if count == 0 {
		return fmt.Errorf("process has 0 matching windows; provide an unambiguous window_title or window_id")
	}
	return fmt.Errorf("process has %d matching windows; retry with window_id (or an unambiguous window_title): %s", count, list)
}

type Result struct {
	PID        int       `json:"pid"`
	WindowID   string    `json:"window_id"`
	Title      string    `json:"title"`
	Elements   []Element `json:"elements,omitempty"`
	Truncated  bool      `json:"truncated,omitempty"`
	Dispatched bool      `json:"dispatched,omitempty"`
	Method     string    `json:"method,omitempty"`
	// EffectObserved is a single immediate readback, not a readiness monitor.
	// Invoke/Toggle/Select do not claim that the application's work completed.
	EffectObserved *bool  `json:"effect_observed,omitempty"`
	Observation    string `json:"observation,omitempty"`
}

func Validate(r Request) error {
	if r.PID <= 0 {
		return fmt.Errorf("a positive process_id is required")
	}
	if r.Action != "inspect_window" && r.Action != "click" && r.Action != "type" {
		return fmt.Errorf("unsupported window action %q", r.Action)
	}
	if utf8.RuneCountInString(r.WindowTitle) > 512 || strings.ContainsRune(r.WindowTitle, 0) {
		return fmt.Errorf("invalid window_title")
	}
	if r.WindowTitle != "" && r.WindowID != "" {
		return fmt.Errorf("choose window_title or window_id")
	}
	if len(r.WindowID) > 32 {
		return fmt.Errorf("invalid window_id")
	}
	if len(r.ElementID) > 64 {
		return fmt.Errorf("invalid element_id")
	}
	if (r.Action == "click" || r.Action == "type") && r.ElementID == "" {
		return fmt.Errorf("element_id from inspect_window is required; no coordinate or mouse fallback")
	}
	if len(r.Text) > 16384 || !utf8.ValidString(r.Text) || strings.ContainsRune(r.Text, 0) {
		return fmt.Errorf("text must be valid UTF-8 without NUL, at most 16384 bytes")
	}
	if r.Action != "type" && r.Text != "" {
		return fmt.Errorf("text is only accepted by type")
	}
	return nil
}

func Birth(pid int) uint64 {
	p, err := Open(pid, 0)
	if err != nil {
		return 0
	}
	defer p.Close()
	return p.Created
}

func Execute(ctx context.Context, p *Process, r Request) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if p == nil {
		return Result{}, fmt.Errorf("window process is unavailable")
	}
	r.Identity = p.Identity
	if err := Validate(r); err != nil {
		return Result{}, err
	}
	if err := p.Alive(); err != nil {
		return Result{}, err
	}
	result, err := executeNative(ctx, r)
	if err != nil {
		return Result{}, err
	}
	if err := p.Alive(); err != nil {
		return Result{}, fmt.Errorf("process exited during window action: %w", err)
	}
	return result, ctx.Err()
}
