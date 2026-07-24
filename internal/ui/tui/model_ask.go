// Package tui is the Bubble Tea presentation layer. F25 replaces
// the raw transcript with a structured chat view (role-based
// colors), adds a status bar, inline event markers, a tool-
// name spinner, Ctrl+C run cancellation, and PgUp/PgDn scrolling.
package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/tools"
)

func (m Model) beginAsk(req tools.AskRequest) (tea.Model, tea.Cmd) {
	if m.mode == modeAsking && m.pendingAsk != nil {
		safeRespond(m.pendingAsk.respond, tools.AskAnswer{Cancelled: true})
	}
	m.pendingAsk = &pendingAsk{
		Question:    req.Question,
		Header:      req.Header,
		Options:     req.Options,
		MultiSelect: req.MultiSelect,
		AllowCustom: req.AllowCustom,
		cursor:      0,
		toggled:     make(map[int]bool),
		respond:     req.Respond,
	}
	m.mode = modeAsking
	m.input.Blur()
	return m, nil
}

// handleAskKey handles key events while mode == modeAsking.
func (m Model) handleAskKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	a := m.pendingAsk
	if a == nil {
		m.mode = modeNormal
		m.input.Focus()
		return m, nil
	}
	if a.customMode {
		switch msg.String() {
		case "esc":
			a.customMode = false
			return m, nil
		case "enter":
			custom := strings.TrimSpace(a.custom)
			if custom == "" {
				return m, nil
			}
			safeRespond(a.respond, tools.AskAnswer{Custom: custom, MultiSelect: a.MultiSelect})
			m.endAsk()
			return m, nil
		case "backspace", "ctrl+h":
			runes := []rune(a.custom)
			if len(runes) > 0 {
				a.custom = string(runes[:len(runes)-1])
			}
			return m, nil
		}
		if len(msg.Runes) > 0 {
			a.custom += string(msg.Runes)
		}
		return m, nil
	}
	switch msg.String() {
	case "esc":
		safeRespond(a.respond, tools.AskAnswer{Cancelled: true})
		m.endAsk()
		return m, nil
	case "enter":
		if a.MultiSelect {
			selected := []string{}
			for i, opt := range a.Options {
				if a.toggled[i] {
					selected = append(selected, opt.Label)
				}
			}
			safeRespond(a.respond, tools.AskAnswer{
				Selected:    selected,
				MultiSelect: true,
			})
		} else {
			idx := a.cursor
			if idx < 0 || idx >= len(a.Options) {
				return m, nil
			}
			safeRespond(a.respond, tools.AskAnswer{
				Selected: []string{a.Options[idx].Label},
			})
		}
		m.endAsk()
		return m, nil
	case "up", "k":
		if a.cursor > 0 {
			a.cursor--
		}
		return m, nil
	case "down", "j":
		if a.cursor < len(a.Options)-1 {
			a.cursor++
		}
		return m, nil
	case " ":
		if a.MultiSelect {
			a.toggled[a.cursor] = !a.toggled[a.cursor]
		}
		return m, nil
	case "c":
		if a.AllowCustom {
			a.customMode = true
		}
		return m, nil
	}
	// Quick-pick: 1-4
	if len(msg.Runes) == 1 {
		r := msg.Runes[0]
		if r >= '1' && r <= '4' {
			idx := int(r - '1')
			if idx >= len(a.Options) {
				return m, nil
			}
			if a.MultiSelect {
				a.toggled[idx] = !a.toggled[idx]
				return m, nil
			}
			safeRespond(a.respond, tools.AskAnswer{
				Selected: []string{a.Options[idx].Label},
			})
			m.endAsk()
			return m, nil
		}
	}
	return m, nil
}

func (m *Model) endAsk() {
	m.pendingAsk = nil
	m.mode = modeNormal
	m.input.Focus()
}

// safeRespond sends ans on ch without blocking.
func safeRespond(ch chan<- tools.AskAnswer, ans tools.AskAnswer) {
	select {
	case ch <- ans:
	default:
	}
}
