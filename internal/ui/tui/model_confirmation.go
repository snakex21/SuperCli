package tui

import (
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/tools"
)

// escapeControls makes terminal controls and bidi formatting visible instead
// of interpreting or silently stripping them: the user must see the actual
// submitted content.
func escapeControls(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (unicode.IsControl(r) && r != '\n') || unicode.Is(unicode.Cf, r) {
			fmt.Fprintf(&b, "\\u%04x", r)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Confirmation has its own interaction rather than ask_user's quick-pick UI.
// Keep all action details reachable, with Cancel selected initially. Neither
// option labels nor the question supplied by a model can enable this mode.
func confirmationDetails(a *pendingAsk, width, height int) (rows []string, slots int, usable bool) {
	if width < 24 || height < 10 {
		return nil, 0, false
	}
	return wrap(escapeControls(a.Question), min(width, 92)-4), height - 8, true
}

func renderConfirmationView(a *pendingAsk, width, height int, language string) string {
	rows, slots, usable := confirmationDetails(a, width, height)
	if !usable {
		message := wrap(textFor(language, "tui.approval.resize"), max(1, width))
		return strings.Join(message[:min(len(message), max(1, height))], "\n")
	}
	inner := min(width, 92) - 4
	start := min(max(0, a.detailOffset), max(0, len(rows)-slots))
	end := min(len(rows), start+slots)
	lines := []string{"┌" + strings.Repeat("─", inner+2) + "┐"}
	row := func(s string) { lines = append(lines, "│ "+padTo(s, inner)+" │") }
	row(textFor(language, "tui.confirm.title"))
	for _, s := range rows[start:end] {
		row(s)
	}
	for i := end - start; i < slots; i++ {
		row("")
	}
	row(fmt.Sprintf(textFor(language, "tui.approval.details"), start+1, end, len(rows)))
	cancel, allow := "> "+textFor(language, "tui.confirm.cancel"), "  "+textFor(language, "tui.confirm.allow")
	if a.cursor == 1 && end == len(rows) {
		cancel, allow = "  "+textFor(language, "tui.confirm.cancel"), "> "+textFor(language, "tui.confirm.allow")
	}
	if end < len(rows) {
		allow = "  " + textFor(language, "tui.confirm.readToAllow")
	}
	row(cancel)
	row(allow)
	keys := textFor(language, "tui.confirm.keys")
	scroll, choose, found := strings.Cut(keys, " · ")
	row(scroll)
	if found {
		row(choose)
	} else {
		row("")
	}
	lines = append(lines, "└"+strings.Repeat("─", inner+2)+"┘")
	return strings.Join(lines, "\n")
}

func (m Model) handleConfirmationKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	a := m.pendingAsk
	var rows []string
	var slots int
	var usable bool
	if a.Approval != nil {
		layout := m.approvalLayout(a)
		rows, slots, usable = layout.rows, layout.slots, layout.usable
	} else {
		rows, slots, usable = confirmationDetails(a, m.width, m.height)
	}
	last := max(0, len(rows)-slots)
	a.detailOffset = min(max(0, a.detailOffset), last)
	canAllow := usable && a.detailOffset+slots >= len(rows)
	// Resizing or scrolling back never leaves an invisible approval selected.
	if !canAllow {
		a.cursor = 0
	}
	if a.Approval != nil {
		return m.handleApprovalKey(msg, a, canAllow, last, slots, len(rows))
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		safeRespond(a.respond, tools.AskAnswer{Cancelled: true})
		m.endAsk()
	case "enter":
		// The answer value is protocol, never the localized label.
		if a.cursor == 1 && canAllow {
			safeRespond(a.respond, tools.AskAnswer{Selected: []string{"Allow once"}})
		} else {
			safeRespond(a.respond, tools.AskAnswer{Cancelled: true})
		}
		m.endAsk()
	case "up", "k":
		a.detailOffset = max(0, a.detailOffset-1)
	case "down", "j":
		a.detailOffset = min(last, a.detailOffset+1)
	case "pgup":
		a.detailOffset = max(0, a.detailOffset-max(1, slots-1))
	case "pgdown":
		a.detailOffset = min(last, a.detailOffset+max(1, slots-1))
	case "home":
		a.detailOffset = 0
	case "end":
		a.detailOffset = last
	case "tab", "shift+tab":
		if canAllow {
			a.cursor = 1 - a.cursor
		}
	}
	if a.detailOffset+slots < len(rows) {
		a.cursor = 0
	}
	return m, nil
}
