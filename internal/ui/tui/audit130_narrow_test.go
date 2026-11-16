package tui

// audit-130: narrow / tiny terminals. Every screen must render without
// panicking, never wider than the terminal and never taller than it.

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"supercli/internal/agent"
	"supercli/internal/tools"
)

var audit130Sizes = []struct{ w, h int }{{40, 15}, {60, 20}, {24, 10}, {20, 8}, {1, 1}, {0, 0}}

func audit130ApprovalReq(lang string) tools.AskRequest {
	approve, deny, similar := "Approve", "Deny", "Approve similar in this session"
	if lang == "pl" {
		approve, deny, similar = "Zatwierdź", "Odrzuć", "Zatwierdź podobne w tej sesji"
	}
	return tools.AskRequest{ID: "ap", Header: "Approval required", Question: "Remove it?", Confirmation: true,
		Respond: make(chan tools.AskAnswer, 1),
		Approval: &tools.ApprovalCard{Tool: "trash", Operation: "trash_outside_workspace", Risk: "write_reversible", Reversible: true,
			Target: `C:\Users\demo\outside\old_notes.txt`, Summary: "Move old_notes.txt to the Trash.",
			ApproveLabel: approve, DenyLabel: deny, SimilarLabel: similar},
		Options: []tools.AskOption{{Label: deny}, {Label: approve}, {Label: similar}}}
}

func audit130ConfirmReq() tools.AskRequest {
	return tools.AskRequest{ID: "cf", Header: "Confirm action", Question: "Run `rm -rf build` in the workspace?", Confirmation: true,
		Respond: make(chan tools.AskAnswer, 1),
		Options: []tools.AskOption{{Label: "Cancel"}, {Label: "Allow once"}}}
}

// audit130Screens builds each screen state at a given size.
func audit130Screens(t *testing.T, lang string, w, h int) map[string]Model {
	t.Helper()
	base := func() Model { return audit130Model(t, lang, w, h) }
	screens := map[string]Model{}
	screens["idle"] = base()

	// Status line wired the way the app does it (Options.StatusFn), then the
	// viewport is re-fitted after the run starts, as refreshTranscript does.
	dir := t.TempDir()
	palette := DefaultPalette()
	busy := New(Options{Home: dir, DataDir: dir, Language: lang, StatusFn: func() string {
		return StatusFn("gpt-5-codex", "12/100", "goal", "", "sess", "1.2k", "$0.01", w, palette, lang)
	}})
	out0, _ := busy.Update(tea.WindowSizeMsg{Width: w, Height: h})
	busy = out0.(Model)
	busy.busy = true
	busy.toolProgress = "read_file · 12/40 files"
	busy.refreshTranscript()
	screens["busy+status"] = busy

	statusOnly := New(Options{Home: dir, DataDir: dir, Language: lang, StatusFn: func() string {
		return StatusFn("gpt-5-codex", "12/100", "goal", "", "sess", "1.2k", "$0.01", w, palette, lang)
	}})
	out0, _ = statusOnly.Update(tea.WindowSizeMsg{Width: w, Height: h})
	screens["idle+status"] = out0.(Model)

	conv := base()
	out, _ := conv.handleAgentEvent(agent.MessageEvent{Text: strings.Repeat("long assistant output ", 30)})
	conv = out.(Model)
	out, _ = conv.handleAgentEvent(agent.DoneEvent{})
	screens["conversation"] = out.(Model)

	screens["approval"] = audit130Begin(t, base(), audit130ApprovalReq(lang))
	screens["confirmation"] = audit130Begin(t, base(), audit130ConfirmReq())
	screens["ask_user"] = audit130Begin(t, base(), sampleRequest())

	set := base()
	out, _ = set.openSettingsMenu()
	screens["settings"] = out.(Model)
	return screens
}

func audit130CheckBounds(view string, w, h int) []string {
	var problems []string
	lines := strings.Split(view, "\n")
	if w > 0 && h > 0 && len(lines) > h {
		problems = append(problems, fmt.Sprintf("%d lines > height %d", len(lines), h))
	}
	if w > 0 {
		for i, line := range lines {
			if lw := ansi.StringWidth(line); lw > w {
				problems = append(problems, fmt.Sprintf("line %d width %d > %d: %q", i, lw, w, ansi.Strip(line)))
				break
			}
		}
	}
	return problems
}

func audit130Render(t *testing.T, name string, m Model) (view string) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s: View() panicked: %v", name, r)
		}
	}()
	return m.View()
}

// Never panic, at any size, in any screen.
func TestAudit130NarrowNoPanic(t *testing.T) {
	for _, lang := range []string{"pl", "en"} {
		for _, s := range audit130Sizes {
			for name, m := range audit130Screens(t, lang, s.w, s.h) {
				audit130Render(t, fmt.Sprintf("%s %dx%d %s", lang, s.w, s.h, name), m)
			}
		}
	}
}

// Modal screens (approval / confirmation / ask) take the whole terminal and
// must fit exactly at every size where they are usable (>= 24x10) and also
// in their "resize" fallback below it.
func TestAudit130NarrowModalsFit(t *testing.T) {
	for _, lang := range []string{"pl", "en"} {
		for _, s := range audit130Sizes {
			if s.w == 0 {
				continue
			}
			screens := audit130Screens(t, lang, s.w, s.h)
			for _, name := range []string{"approval", "confirmation"} {
				view := audit130Render(t, name, screens[name])
				if p := audit130CheckBounds(view, s.w, s.h); len(p) > 0 {
					t.Errorf("%s %dx%d %s: %v", lang, s.w, s.h, name, p)
				}
			}
		}
	}
}

// The ask_user quick-pick and settings menu at narrow widths.
func TestAudit130NarrowAskAndSettingsFit(t *testing.T) {
	var problems []string
	for _, lang := range []string{"pl", "en"} {
		for _, s := range audit130Sizes {
			if s.w == 0 {
				continue
			}
			screens := audit130Screens(t, lang, s.w, s.h)
			for _, name := range []string{"ask_user", "settings"} {
				if p := audit130CheckBounds(audit130Render(t, name, screens[name]), s.w, s.h); len(p) > 0 {
					problems = append(problems, fmt.Sprintf("%s %dx%d %s: %v", lang, s.w, s.h, name, p))
				}
			}
		}
	}
	if len(problems) > 0 {
		t.Fatalf("overflow:\n%s", strings.Join(problems, "\n"))
	}
}

// The main chat screen (header, transcript, status, composer, hints).
func TestAudit130NarrowMainScreenFits(t *testing.T) {
	var problems []string
	for _, lang := range []string{"pl", "en"} {
		for _, s := range audit130Sizes {
			if s.w == 0 {
				continue
			}
			screens := audit130Screens(t, lang, s.w, s.h)
			for _, name := range []string{"idle", "idle+status", "busy+status", "conversation"} {
				if p := audit130CheckBounds(audit130Render(t, name, screens[name]), s.w, s.h); len(p) > 0 {
					problems = append(problems, fmt.Sprintf("%s %dx%d %s: %v", lang, s.w, s.h, name, p))
				}
			}
		}
	}
	if len(problems) > 0 {
		t.Fatalf("overflow:\n%s", strings.Join(problems, "\n"))
	}
}

// Approval card at 40x15 and 60x20 still offers the [n]/[y] choices.
func TestAudit130NarrowApprovalShowsChoices(t *testing.T) {
	for _, lang := range []string{"pl", "en"} {
		for _, s := range []struct{ w, h int }{{40, 15}, {60, 20}} {
			m := audit130Screens(t, lang, s.w, s.h)["approval"]
			view := ansi.Strip(m.View())
			for _, want := range []string{"[n]", "[y]", "[a]"} {
				if !strings.Contains(view, want) {
					t.Errorf("%s %dx%d: %s missing:\n%s", lang, s.w, s.h, want, view)
				}
			}
			// The card is reachable: End then y approves.
			m = audit130Key(m, "end")
			m = audit130Key(m, "y")
			req := m.pendingAsk
			if req != nil {
				t.Errorf("%s %dx%d: y after End did not answer the card", lang, s.w, s.h)
			}
		}
	}
}

// The bottom-edge key hint of the approval card ("... · y/n/a") must stay
// readable at 40 columns: it is the only place that says what Enter/Tab do.
func TestAudit130NarrowApprovalKeyHintVisible(t *testing.T) {
	var problems []string
	for _, lang := range []string{"pl", "en"} {
		for _, s := range []struct{ w, h int }{{40, 15}, {60, 20}} {
			view := ansi.Strip(audit130Screens(t, lang, s.w, s.h)["approval"].View())
			lines := strings.Split(view, "\n")
			bottom := lines[len(lines)-1]
			if !strings.Contains(bottom, "y/n/a") {
				problems = append(problems, fmt.Sprintf("%s %dx%d bottom edge: %q", lang, s.w, s.h, bottom))
			}
		}
	}
	if len(problems) > 0 {
		t.Fatalf("key hint truncated:\n%s", strings.Join(problems, "\n"))
	}
}

// Generic confirmation (no approval card) at 40x15: the user must be able
// to see how to allow (Tab/Enter) and both choices.
func TestAudit130NarrowConfirmationUsable(t *testing.T) {
	for _, lang := range []string{"pl", "en"} {
		m := audit130Screens(t, lang, 40, 15)["confirmation"]
		view := ansi.Strip(m.View())
		cancel, allow := textFor(lang, "tui.confirm.cancel"), textFor(lang, "tui.confirm.allow")
		for _, want := range []string{cancel, allow, "Tab", "Enter"} {
			if !strings.Contains(view, want) {
				t.Errorf("%s 40x15 confirmation lacks %q:\n%s", lang, want, view)
			}
		}
		for i, line := range strings.Split(view, "\n") {
			if strings.Contains(line, "…") {
				t.Errorf("%s 40x15 confirmation row %d truncated: %q", lang, i, line)
			}
		}
		// Tab then Enter allows once (protocol label).
		ch := m.pendingAsk.respond
		out, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
		out, _ = out.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
		_ = out
		select {
		case got := <-ch:
			if len(got.Selected) != 1 || got.Selected[0] != "Allow once" {
				t.Errorf("%s: Tab+Enter answered %+v", lang, got)
			}
		default:
			t.Errorf("%s: Tab+Enter did not answer", lang)
		}
	}
}

// The busy status row must stay one row and keep the spinner label visible.
func TestAudit130NarrowStatusLine(t *testing.T) {
	for _, lang := range []string{"pl", "en"} {
		for _, w := range []int{40, 60} {
			m := audit130Screens(t, lang, w, 20)["busy+status"]
			view := ansi.Strip(m.View())
			label := textFor(lang, "tui.model_view.2a7749f651")
			found := false
			for _, line := range strings.Split(view, "\n") {
				if strings.Contains(line, strings.TrimSpace(strings.Split(label, "·")[0])) {
					found = true
				}
				if ansi.StringWidth(line) > w {
					t.Errorf("%s %d: status overflow %q", lang, w, line)
				}
			}
			if !found {
				t.Errorf("%s %d: busy label %q not visible:\n%s", lang, w, label, view)
			}
		}
	}
}
