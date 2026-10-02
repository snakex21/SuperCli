package tui

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/tools"
)

func keyDispatchTestModel(t *testing.T) Model {
	t.Helper()
	m := New(Options{Home: t.TempDir(), NoColor: true, Language: "en"})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 36})
	return next.(Model)
}

func TestKeyDispatchKeepsInputModesAndIgnoredAltKeys(t *testing.T) {
	m := keyDispatchTestModel(t)
	height := m.viewport.Height
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("draft")})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}, Alt: true})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'ą'}, Alt: true})
	m = next.(Model)
	if m.input.Value() != "draftą" || m.viewport.Height != height {
		t.Fatal("ordinary typing or AltGr changed input/geometry")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	m = next.(Model)
	if m.mode != modeMenu || m.menu.kind != menuReasoning || m.input.Focused() {
		t.Fatal("reasoning key did not open the menu")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	if m.input.Value() != "draftą" {
		t.Fatal("menu key reached the composer")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.mode != modeNormal || !m.input.Focused() || m.input.Value() != "draftą" {
		t.Fatal("closing the menu lost composer focus or draft")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.input.Value() != "" || m.viewport.Height != height {
		t.Fatal("composer Escape failed to clear and restore geometry")
	}
}

func TestKeyDispatchKeepsFocusedStyleOwnershipAndCopiedStyles(t *testing.T) {
	original := keyDispatchTestModel(t)
	original.input.SetValue("draft")
	original.endAsk()
	suffix := "first"
	original.input.FocusedStyle.Prompt = original.input.FocusedStyle.Prompt.Transform(func(s string) string { return s + suffix })
	if !strings.Contains(original.input.View(), "first") {
		t.Fatal("replacement after focus was hidden")
	}
	changed := original
	changed.input.FocusedStyle.Prompt = changed.input.FocusedStyle.Prompt.Transform(func(s string) string { return s + "copy" })
	changed.endAsk()
	if !strings.Contains(changed.input.View(), "copy") || strings.Contains(original.input.View(), "copy") {
		t.Fatal("copied widget styles were not independently replaceable")
	}
	next, _ := original.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	original = next.(Model)
	suffix = "second"
	if !strings.Contains(original.input.View(), "second") {
		t.Fatal("live style transform was frozen during dispatch")
	}
}

func TestKeyDispatchBatchesKeepDraftRecoveryAndEventOrder(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	recovery, err := OpenDraftRecovery(data, home)
	if err != nil {
		t.Fatal(err)
	}
	defer recovery.Flush()
	m := New(Options{Home: home, DataDir: data, SessionID: "fixture", DraftRecovery: recovery, NoColor: true, Language: "en"})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 36})
	m = next.(Model)
	next, _ = m.Update(terminalKeyBatchMsg{{Type: tea.KeyRunes, Runes: []rune("żółć")}, {Type: tea.KeyBackspace}, {Type: tea.KeyEnter, Alt: true}, {Type: tea.KeyRunes, Runes: []rune("漢字")}})
	m = next.(Model)
	if got := m.input.Value(); got != "żół\n漢字" || m.input.Height() != 2 {
		t.Fatalf("key batch lost ordering/newline: %q", got)
	}
	if got := recovery.Snapshot(); got.SessionID != "fixture" || got.Text != m.input.Value() {
		t.Fatalf("key batch did not persist its final draft: %+v", got)
	}
	next, _ = m.Update(terminalInputBatchMsg{tea.WindowSizeMsg{Width: 60, Height: 30}, tea.KeyMsg{Type: tea.KeyEsc}, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("next")}})
	m = next.(Model)
	if m.input.Value() != "next" || m.input.Height() != 1 || m.viewport.Width != 60 {
		t.Fatal("mixed event batch did not apply resize/clear/text in order")
	}
	if err := recovery.Flush(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenDraftRecovery(data, home)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Snapshot(); !reflect.DeepEqual(got, DraftSnapshot{SessionID: "fixture", Text: "next"}) {
		t.Fatalf("reloaded draft differs: %+v", got)
	}
}

func TestKeyDispatchKeepsAskAndBusyInputSeparate(t *testing.T) {
	m := keyDispatchTestModel(t)
	m.input.SetValue("unsent draft")
	answers := make(chan tools.AskAnswer, 1)
	next, _ := m.Update(askRequestMsg{req: tools.AskRequest{ID: "fixture", Question: "pick", Options: []tools.AskOption{{Label: "yes"}}, Respond: answers}})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = next.(Model)
	select {
	case answer := <-answers:
		if !reflect.DeepEqual(answer.Selected, []string{"yes"}) {
			t.Fatalf("wrong answer: %+v", answer)
		}
	default:
		t.Fatal("ask key reached the composer instead of the question")
	}
	if m.mode != modeNormal || !m.input.Focused() || m.input.Value() != "unsent draft" {
		t.Fatal("answering changed the draft or failed to restore focus")
	}
	m.busy = true
	m.input.CursorEnd()
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'E'}})
	m = next.(Model)
	if !m.busy || !m.toolExpanded || m.input.Value() != "unsent drafta" {
		t.Fatal("busy typing/tool expansion changed dispatch mode or inserted the shortcut")
	}
}

func BenchmarkTUIKeyDispatch(b *testing.B) {
	for _, name := range []string{"escape", "escape-with-recovery", "typing-pair", "status-refresh"} {
		b.Run(name, func(b *testing.B) {
			m := New(Options{Home: b.TempDir(), NoColor: true, Language: "en"})
			next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 36})
			m = next.(Model)
			if name == "escape-with-recovery" {
				m.drafts = &DraftRecovery{}
			}
			if name == "typing-pair" {
				m.input.SetValue("A short unsent draft")
				m.input.CursorEnd()
				m.syncInputHeight()
			}
			runeKey := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}
			backKey := tea.KeyMsg{Type: tea.KeyBackspace}
			escapeKey := tea.KeyMsg{Type: tea.KeyEsc}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				switch name {
				case "typing-pair":
					next, _ = m.Update(runeKey)
					m = next.(Model)
					next, _ = m.Update(backKey)
				case "status-refresh":
					next, _ = m.Update(statusRefreshMsg{})
				default:
					next, _ = m.Update(escapeKey)
				}
				m = next.(Model)
			}
			if m.drafts != nil && m.drafts.timer != nil {
				b.Fatal("unchanged recovery unexpectedly started a save timer")
			}
		})
	}
}
