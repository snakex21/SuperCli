package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"supercli/internal/tools"
)

func confirmationModel(t *testing.T, width, height int, question string) (Model, chan tools.AskAnswer) {
	t.Helper()
	req := sampleRequest()
	req.Question = question
	req.Confirmation = true
	req.Options = []tools.AskOption{{Label: "Cancel"}, {Label: "Allow once"}}
	m := New(Options{Home: t.TempDir()})
	m.width, m.height = width, height
	out, _ := m.beginAsk(req)
	return out.(Model), req.Respond
}
func confirmationKey(m Model, key tea.KeyType) Model {
	out, _ := m.handleAskKey(tea.KeyMsg{Type: key})
	return out.(Model)
}
func TestConfirmationFullLongDetailsReachable(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {40, 12}, {24, 10}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			var details strings.Builder
			details.WriteString("Provider: fal\nModel: configured\nCharges: possible\nPrompt:\n")
			for i := 0; i < 500; i++ {
				fmt.Fprintf(&details, "prompt-line-%03d\n", i)
			}
			details.WriteString("MCP arguments:\n{\"target\":\"END-123\"}\nParameters: FINAL")
			m, ch := confirmationModel(t, size[0], size[1], details.String())
			seen := ""
			for {
				view := m.View()
				seen += view + "\n"
				lines := strings.Split(view, "\n")
				if len(lines) > size[1] {
					t.Fatalf("height overflow: %d", len(lines))
				}
				for _, line := range lines {
					if ansi.StringWidth(line) > size[0] {
						t.Fatalf("width overflow: %q", line)
					}
				}
				prev := m.pendingAsk.detailOffset
				m = confirmationKey(m, tea.KeyPgDown)
				if m.pendingAsk.detailOffset == prev {
					break
				}
			}
			for i := 0; i < 500; i++ {
				if !strings.Contains(seen, fmt.Sprintf("prompt-line-%03d", i)) {
					t.Fatalf("hidden prompt line %d", i)
				}
			}
			for _, s := range []string{"Provider: fal", "Charges: possible", "MCP arguments:", "END-123", "Parameters: FINAL"} {
				if !strings.Contains(seen, s) {
					t.Fatalf("hidden detail %q", s)
				}
			}
			m = confirmationKey(m, tea.KeyTab)
			m = confirmationKey(m, tea.KeyEnter)
			if ans := <-ch; len(ans.Selected) != 1 || ans.Selected[0] != "Allow once" {
				t.Fatalf("explicit allow failed: %+v", ans)
			}
		})
	}
}
func TestConfirmationDefaultsToCancelAndRejectsQuickPick(t *testing.T) {
	for _, question := range []string{"short action", strings.Repeat("long prompt\n", 100)} {
		m, ch := confirmationModel(t, 80, 24, question)
		out, _ := m.handleAskKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
		m = out.(Model)
		select {
		case ans := <-ch:
			t.Fatalf("quick pick acted: %+v", ans)
		default:
		}
		m = confirmationKey(m, tea.KeyEnter)
		if ans := <-ch; !ans.Cancelled {
			t.Fatalf("Enter approved by default: %+v", ans)
		}
	}
}
func TestConfirmationCannotApproveUnreadOrTinyTerminal(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {23, 24}, {80, 9}, {1, 1}} {
		m, ch := confirmationModel(t, size[0], size[1], strings.Repeat("MCP arguments long\n", 100))
		m = confirmationKey(m, tea.KeyTab)
		m = confirmationKey(m, tea.KeyEnter)
		if ans := <-ch; !ans.Cancelled {
			t.Fatalf("unread/tiny approval at %v: %+v", size, ans)
		}
	}
}
func TestConfirmationScrollBackAndResizeRevokeSelection(t *testing.T) {
	for _, resize := range []bool{false, true} {
		m, ch := confirmationModel(t, 80, 24, strings.Repeat("line\n", 100))
		m = confirmationKey(m, tea.KeyEnd)
		m = confirmationKey(m, tea.KeyTab)
		if m.pendingAsk.cursor != 1 {
			t.Fatal("allow not selected")
		}
		if resize {
			m.width = 10
		} else {
			m = confirmationKey(m, tea.KeyHome)
		}
		m = confirmationKey(m, tea.KeyEnter)
		if ans := <-ch; !ans.Cancelled {
			t.Fatal("hidden selection approved")
		}
	}
}
func TestConfirmationEscapesTerminalAndBidiControls(t *testing.T) {
	m, _ := confirmationModel(t, 80, 24, "prompt: \x1b[2J\r\b\u202eimportant")
	view := m.View()
	for _, s := range []string{`\u001b[2J`, `\u000d`, `\u0008`, `\u202eimportant`} {
		if !strings.Contains(view, s) {
			t.Fatalf("missing visible escape %q: %s", s, view)
		}
	}
	if strings.ContainsAny(view, "\x1b\r\b\u202e") {
		t.Fatal("terminal control escaped into view")
	}
}
func TestQueuedConfirmationRetainsTrustedMode(t *testing.T) {
	m, _ := confirmationModel(t, 80, 24, "first")
	req := sampleRequest()
	req.Confirmation = true
	req.Question = "queued"
	out, _ := m.beginAsk(req)
	m = out.(Model)
	m = confirmationKey(m, tea.KeyEsc)
	if m.pendingAsk == nil || !m.pendingAsk.Confirmation || m.pendingAsk.cursor != 0 {
		t.Fatal("queued confirmation lost safe state")
	}
	m = confirmationKey(m, tea.KeyEnter)
	if ans := <-req.Respond; !ans.Cancelled {
		t.Fatal("queued Enter approved")
	}
}
