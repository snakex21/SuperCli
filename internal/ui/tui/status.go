package tui

import (
	"fmt"
	"strings"
)

// StatusBar renders the single-line status footer below the viewport.
// It shows: model | credits | goal | draft | session — each section
// separated by a dim pipe. Empty sections are skipped.
type StatusBar struct {
	Model      string // e.g. "gpt-4o"
	Credits    string // e.g. "1.2k/10k (12%)"
	Goal       string // e.g. "3/5 tasks"
	DraftMode  string // e.g. "draft: on"
	Session    string // e.g. "abc123"
	Tokens     string // F34: e.g. "1.2k"
	Cost       string // F34: e.g. "$0.003"
	Width      int
}

// Render produces the status line. Sections flow left-to-right;
// the line is truncated to Width if necessary.
func (s StatusBar) Render(p Palette) string {
	var parts []string

	if s.Model != "" {
		parts = append(parts, s.renderSection("model", s.Model, p))
	}
	if s.Credits != "" {
		parts = append(parts, s.renderSection("cr", s.Credits, p))
	}
	if s.Goal != "" {
		parts = append(parts, s.renderSection("goal", s.Goal, p))
	}
	if s.DraftMode != "" {
		parts = append(parts, s.renderSection("draft", s.DraftMode, p))
	}
	if s.Session != "" {
		parts = append(parts, s.renderSection("sess", s.Session, p))
	}
	// F34: live token counter and cost.
	if s.Tokens != "" || s.Cost != "" {
		val := ""
		if s.Tokens != "" && s.Cost != "" {
			val = s.Tokens + " │ " + s.Cost
		} else if s.Tokens != "" {
			val = s.Tokens
		} else {
			val = s.Cost
		}
		parts = append(parts, s.renderSection("tok", val, p))
	}

	sep := p.StatusSep.Render(" │ ")
	line := strings.Join(parts, sep)

	// Truncate if wider than terminal.
	if s.Width > 0 && len([]rune(line)) > s.Width {
		line = string([]rune(line)[:s.Width-1]) + "…"
	}
	return line
}

// renderSection formats one section as "key: value" with
// key in StatusKey style and value in StatusValue style.
func (s StatusBar) renderSection(key, value string, p Palette) string {
	return p.StatusKey.Render(key) + p.StatusDim.Render(":") + " " + p.StatusValue.Render(value)
}

// StatusFn builds a StatusBar from the given fields and renders it.
// This is a convenience for main.go's statusFn wiring.
func StatusFn(model, credits, goal, draftMode, session, tokens, cost string, width int, p Palette) string {
	sb := StatusBar{
		Model:     model,
		Credits:   credits,
		Goal:      goal,
		DraftMode: draftMode,
		Session:   session,
		Tokens:    tokens,
		Cost:      cost,
		Width:     width,
	}
	return sb.Render(p)
}

// FormatCredits formats the credit numbers into a compact string.
// Example: "1200/10000 (12%)".
func FormatCredits(used, total int) string {
	if total <= 0 {
		return ""
	}
	pct := used * 100 / total
	return fmt.Sprintf("%s/%s (%d%%)", compactNum(used), compactNum(total), pct)
}

// compactNum formats large numbers as "1.2k" etc.
func compactNum(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fm", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
