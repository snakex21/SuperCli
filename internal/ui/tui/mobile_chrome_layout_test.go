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

func TestMobileChatChromeFitsKeyboardViewport(t *testing.T) {
	r := lipgloss.NewRenderer(io.Discard, termenv.WithProfile(termenv.TrueColor))
	r.SetColorProfile(termenv.TrueColor)
	r.SetHasDarkBackground(true)
	for _, size := range [][2]int{{32, 12}, {38, 14}, {42, 28}, {72, 12}, {60, 20}, {110, 34}} {
		for _, state := range []string{"welcome", "chat", "busy", "multiline", "attachment", "autocomplete"} {
			m := mobileLayoutFixture(t, size[0], size[1], state, "pl", r)
			switch state {
			case "multiline":
				m.input.SetValue("Pierwsza linia\nDruga linia\nTrzecia linia")
				m.syncInputHeight()
			case "attachment":
				m.pendingAttachments = []string{"~/projects/długi-załącznik-do-testu.png"}
			case "autocomplete":
				m.input.SetValue("/")
				m.updateAutocompleteState()
			}
			m.resizeViewport()
			view := m.View()
			if rows := lipgloss.Height(view); rows > size[1] {
				t.Errorf("%v %s: %d rows exceed terminal height %d (input=%d, inputView=%d, box=%d, chrome=%d, dashboard=%d, viewport=%d)", size, state, rows, size[1], m.input.Height(), lipgloss.Height(m.input.View()), lipgloss.Height(m.renderInputBox()), m.chromeHeight(), m.dashboardHeight(), m.viewport.Height)
			}
			for i, row := range strings.Split(view, "\n") {
				if width := ansi.StringWidth(row); width > size[0] {
					t.Errorf("%v %s: row %d width %d exceeds %d: %q", size, state, i+1, width, size[0], row)
				}
			}
			if !strings.Contains(view, m.renderInputBox()) || !strings.HasSuffix(view, m.renderHintLine()) {
				t.Errorf("%v %s: input or keyboard hints were clipped", size, state)
			}
			dashboard := m.renderDashboard()
			rows := 0
			if dashboard != "" {
				rows = lipgloss.Height(dashboard)
			}
			if size[1] < 20 && rows > 3 {
				t.Errorf("%v %s: %d dashboard rows crowd a keyboard-sized screen", size, state, rows)
			}
			if rows != m.dashboardHeight() {
				t.Errorf("%v %s: reserved dashboard %d rows, rendered %d", size, state, m.dashboardHeight(), rows)
			}
		}
	}
}

func TestMobileWelcomeFitsChatArea(t *testing.T) {
	r := lipgloss.NewRenderer(io.Discard, termenv.WithProfile(termenv.TrueColor))
	r.SetColorProfile(termenv.TrueColor)
	for _, size := range [][2]int{{32, 12}, {38, 14}, {42, 28}, {72, 12}, {60, 20}, {110, 34}} {
		m := mobileLayoutFixture(t, size[0], size[1], "welcome", "pl", r)
		if m.viewport.TotalLineCount() > m.viewport.Height || !m.viewport.AtBottom() {
			t.Errorf("%v: empty state was cropped or shown as scroll-back history", size)
		}
		if !strings.Contains(ansi.Strip(m.viewport.View()), "Tab") {
			t.Errorf("%v: welcome actions disappeared", size)
		}
		if size[0] >= 100 && size[1] >= 30 && !strings.Contains(m.viewport.View(), "╭") {
			t.Errorf("%v: roomy desktop lost the welcome cards", size)
		}
	}
}

func TestMobileDashboardCacheTracksComposerHeight(t *testing.T) {
	r := lipgloss.NewRenderer(io.Discard, termenv.WithProfile(termenv.TrueColor))
	r.SetColorProfile(termenv.TrueColor)
	m := mobileLayoutFixture(t, 38, 14, "chat", "pl", r)
	m.input.SetValue("Krótka wiadomość")
	m.syncInputHeight()
	before := m.renderDashboard()
	m.input.SetValue("Pierwsza\nDruga\nTrzecia\nCzwarta")
	m.syncInputHeight()
	got := m.renderDashboard()
	fresh := m
	fresh.renderCache = nil
	if got != fresh.renderDashboard() {
		t.Fatal("dashboard cache retained the larger layout after composer expansion")
	}
	if got == before {
		t.Fatal("fixture did not compact the dashboard for a taller composer")
	}
}

func TestMobileCombinedComposerStates(t *testing.T) {
	r := lipgloss.NewRenderer(io.Discard, termenv.WithProfile(termenv.TrueColor))
	r.SetColorProfile(termenv.TrueColor)
	for _, size := range [][2]int{{32, 12}, {48, 12}, {38, 14}} {
		m := mobileLayoutFixture(t, size[0], size[1], "chat", "pl", r)
		draft := "Pierwsza\nDruga\nTrzecia\nCzwarta\nOstatnia"
		m.input.SetValue(draft)
		m.input.CursorEnd()
		m.syncInputHeight()
		_ = m.renderInputBox()
		m.busy = true
		m.pendingAttachments = []string{"fixture.png"}
		m.resizeViewport()
		if lipgloss.Height(m.View()) > size[1] || m.input.Value() != draft {
			t.Errorf("%v: a tall draft, running task and attachment overflowed or lost text", size)
		}
		if !strings.Contains(ansi.Strip(m.renderInputBox()), "Ostatnia") {
			t.Errorf("%v: shrinking the composer hid its active cursor line", size)
		}
		m.busy = false
		m.pendingAttachments = nil
		m.resizeViewport()
		if m.input.Height() != 5 || m.input.Value() != draft {
			t.Errorf("%v: the composer did not restore its height and draft", size)
		}
	}
}

func TestMobileResizeKeepsDraftAndScrollIntent(t *testing.T) {
	r := lipgloss.NewRenderer(io.Discard, termenv.WithProfile(termenv.ANSI256))
	r.SetColorProfile(termenv.ANSI256)
	m := mobileLayoutFixture(t, 110, 34, "chat", "pl", r)
	draft := m.input.Value()
	m.viewport.GotoTop()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 38, Height: 14})
	m = next.(Model)
	if m.input.Value() != draft || m.viewport.YOffset != 0 || lipgloss.Height(m.View()) > 14 {
		t.Fatal("opening the keyboard changed the draft, scroll intent or frame bounds")
	}
	m.viewport.GotoBottom()
	next, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
	m = next.(Model)
	if m.input.Value() != draft || !m.viewport.AtBottom() {
		t.Fatal("closing the keyboard lost the draft or follow mode")
	}
}
