package tui

import (
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestTranscriptFoldRefreshesOnMenuClose(t *testing.T) {
	for _, key := range []tea.KeyType{tea.KeyEsc, tea.KeyCtrlK} {
		m := New(Options{NoColor: true, Language: "en"})
		m.width, m.height = 80, 36
		m.viewport.Width = 80
		m.input.SetWidth(80)
		m.chat.width = 80
		m.chat.addAssistant("needle heading\nNEEDLE HIDDEN SECOND LINE")
		m.refreshTranscript()
		next, _ := m.openTranscriptSearchMenu()
		m = next.(Model)
		m.menu.filter = "needle"
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
		m = next.(Model)
		if !m.chat.msgs[0].collapsed || !m.chat.completedDirty {
			t.Fatal("fixture did not fold the message")
		}
		next, _ = m.Update(tea.KeyMsg{Type: key})
		m = next.(Model)
		if m.mode != modeNormal {
			t.Fatal("search menu did not close")
		}
		if strings.Contains(m.viewport.View(), "NEEDLE HIDDEN SECOND LINE") || m.chat.completedDirty {
			t.Fatal("closed menu retained unfolded transcript")
		}
		if !strings.Contains(m.viewport.View(), "collapsed") {
			t.Fatal("closed menu did not show the folded message")
		}
	}
}

func TestTranscriptFoldMenuClosePreservesManualScroll(t *testing.T) {
	m := New(Options{NoColor: true, Language: "en"})
	m.width, m.height = 80, 36
	m.viewport.Width = 80
	m.input.SetWidth(80)
	m.chat.width = 80
	m.chat.addAssistant("needle heading\nhidden second line")
	for i := 0; i < 30; i++ {
		m.chat.addAssistant("following answer\nsecond line\nthird line")
	}
	m.refreshTranscript()
	m.viewport.SetYOffset(3)
	if m.viewport.AtBottom() {
		t.Fatal("fixture must start away from the tail")
	}
	next, _ := m.openTranscriptSearchMenu()
	m = next.(Model)
	m.menu.filter = "needle"
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.viewport.YOffset != 3 || m.viewport.AtBottom() {
		t.Fatal("closing a folded search result moved manual scroll to the tail")
	}
}

func TestChatRenderedLineMatchesWrappedTranscript(t *testing.T) {
	palettes := []Palette{NoColorPalette()}
	renderer := lipgloss.NewRenderer(io.Discard)
	renderer.SetColorProfile(termenv.TrueColor)
	renderer.SetHasDarkBackground(true)
	palettes = append(palettes, NewPalette(renderer))
	cases := []struct {
		name            string
		width           int
		previous        msg
		target          role
		tools, thinking bool
	}{
		{"expanded tool wrap", 30, msg{role: roleSystem, toolName: "read_file", text: strings.Repeat("1234567890 ", 30)}, roleSystem, true, false},
		{"folded tool wrap", 30, msg{role: roleSystem, toolName: "read_file", text: strings.Repeat("wide output ", 30)}, roleSystem, false, false},
		{"assistant turn separator", 80, msg{role: roleSystem, text: "prior event"}, roleAssistant, false, false},
		{"user after document", 25, msg{role: roleDocument, text: strings.Repeat("long document ", 20)}, roleUser, false, false},
		{"individual folded wrap", 17, msg{role: roleAssistant, text: strings.Repeat("folded heading ", 15) + "\nsecond", collapsed: true}, roleSystem, false, false},
		{"thinking folded", 25, msg{role: roleAssistant, text: "<thinking>Long thought 中文 😀.</thinking>\nA **formatted** answer."}, roleUser, false, true},
		{"unicode ANSI wrap", 19, msg{role: roleAssistant, text: strings.Repeat("**日本語** 😀 中文 ", 12)}, roleAssistant, false, false},
	}
	for pi, p := range palettes {
		for _, tc := range cases {
			t.Run(tc.name+"/"+[]string{"plain", "truecolor"}[pi], func(t *testing.T) {
				c := newChat(tc.width, "en")
				c.legacySymbols = false
				c.toolsExpanded = tc.tools
				c.thinkingCollapsed = tc.thinking
				c.msgs = append(c.msgs, tc.previous, msg{role: tc.target, text: "TARGET SEARCH ROW"})
				c.completedDirty = true
				full := c.renderCompleted(p)
				target := ansi.Wrap(c.renderMsg(c.msgs[1], p), max(1, c.width), "")
				offset := strings.Index(full, target)
				if offset < 0 {
					t.Fatal("target block absent from full transcript")
				}
				want := strings.Count(full[:offset], "\n")
				if got := c.renderedLineForMessage(1, p); got != want {
					t.Fatalf("rendered target row=%d, full transcript row=%d", got, want)
				}
				if c.renderedLineForMessage(0, p) != 0 || c.renderedLineForMessage(-1, p) != 0 {
					t.Fatal("first/negative index must start at row zero")
				}
				if got, want := c.renderedLineForMessage(99, p), strings.Count(full, "\n"); got != want {
					t.Fatalf("past-end row=%d, full transcript end=%d", got, want)
				}
			})
		}
	}
}

func TestCloseMenuPreservesWelcomeWithDirtyEmptyChat(t *testing.T) {
	m := New(Options{NoColor: true, Language: "en"})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(Model)
	if !m.chat.completedDirty || m.chat.len() != 0 || m.current != "" {
		t.Fatal("fixture must retain a dirty empty-chat welcome")
	}
	before := m.viewport.View()
	m.enterMenu(interactiveMenu{kind: menuActions})
	next, _ = m.closeMenu()
	m = next.(Model)
	if m.mode != modeNormal || m.viewport.View() != before {
		t.Fatal("closing an empty conversation menu replaced its welcome")
	}
}

func TestTranscriptFoldRefreshesAfterReturningThroughParentMenu(t *testing.T) {
	m := New(Options{NoColor: true, Language: "en"})
	m.width, m.height = 80, 36
	m.viewport.Width = 80
	m.input.SetWidth(80)
	m.chat.width = 80
	m.chat.addAssistant("needle heading\nNEEDLE HIDDEN SECOND LINE")
	m.refreshTranscript()
	m.enterMenu(interactiveMenu{kind: menuActions, filter: "keep parent search"})
	next, _ := m.openTranscriptSearchMenu()
	m = next.(Model)
	m.menu.filter = "needle"
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.menu.kind != menuActions || m.menu.filter != "keep parent search" {
		t.Fatal("search fold did not preserve parent menu")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.mode != modeNormal || strings.Contains(m.viewport.View(), "NEEDLE HIDDEN SECOND LINE") || m.chat.completedDirty {
		t.Fatal("returning through a parent menu retained an unfolded viewport")
	}
}
