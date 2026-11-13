package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/tools"
)

// The host approval prompt reuses the confirmation view (details scroll, Deny
// focused first, approval only after the details were read to the end) and
// adds direct keys: y approve once, a approve similar in this session, n/Esc
// deny. Labels come from the trusted approval card, never from the model.

func approvalTitle(a *pendingAsk) string {
	if a.Header != "" {
		return a.Header + " (y/n/a)"
	}
	return "Approval required (y/n/a)"
}

func approvalLabels(a *pendingAsk) []string {
	labels := []string{a.Approval.DenyLabel, a.Approval.ApproveLabel}
	if a.Approval.SimilarLabel != "" {
		labels = append(labels, a.Approval.SimilarLabel)
	}
	return labels
}

func approvalChoiceRows(a *pendingAsk, canAllow bool) []string {
	keys := []string{"n", "y", "a"}
	var rows []string
	for i, label := range approvalLabels(a) {
		marker := "  "
		if i == a.cursor {
			marker = "> "
		}
		if i > 0 && !canAllow {
			label = "(read to end) " + label
		}
		rows = append(rows, marker+"["+keys[i]+"] "+label)
	}
	return append(rows, "↑↓ PgUp/Dn; Tab; Enter; y/n/a")
}

func (m Model) handleApprovalKey(msg tea.KeyMsg, a *pendingAsk, canAllow bool, last, slots, rows int) (tea.Model, tea.Cmd) {
	labels := approvalLabels(a)
	choose := func(i int) {
		if i <= 0 || i >= len(labels) || !canAllow {
			safeRespond(a.respond, tools.AskAnswer{Cancelled: true})
		} else {
			safeRespond(a.respond, tools.AskAnswer{Selected: []string{labels[i]}})
		}
		m.endAsk()
	}
	switch msg.String() {
	case "esc", "ctrl+c", "n", "N":
		choose(0)
		return m, nil
	case "y", "Y":
		if canAllow {
			choose(1)
		}
		return m, nil
	case "a", "A":
		if canAllow && len(labels) > 2 {
			choose(2)
		}
		return m, nil
	case "enter":
		choose(a.cursor)
		return m, nil
	case "tab", "right":
		if canAllow {
			a.cursor = (a.cursor + 1) % len(labels)
		}
	case "shift+tab", "left":
		if canAllow {
			a.cursor = (a.cursor + len(labels) - 1) % len(labels)
		}
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
	}
	if a.detailOffset+slots < rows {
		a.cursor = 0
	}
	return m, nil
}
