package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"strings"
	"supercli/internal/agent"
	"supercli/internal/storage/goal"
	"testing"
)

func TestNativeReasoningIsOneBlockAndToolOrderIsChronological(t *testing.T) {
	m := streamTestModel(t)
	for _, text := range []string{"Need ", "to inspect ", "the file."} {
		next, _ := m.Update(runEventMsg{ev: agent.ReasoningEvent{Text: text}})
		m = next.(Model)
	}
	if strings.Count(m.current, "<thinking>") != 1 {
		t.Fatal(m.current)
	}
	next, _ := m.Update(runEventMsg{ev: agent.ToolCallEvent{Name: "read_file", ID: "r", Args: "{}"}})
	m = next.(Model)
	next, _ = m.Update(runEventMsg{ev: agent.ToolResultEvent{ID: "r", Output: "found"}})
	m = next.(Model)
	next, _ = m.Update(runEventMsg{ev: agent.MessageEvent{Text: "The answer is here."}})
	m = next.(Model)
	m.refreshTranscript()
	text := m.chat.renderWithSpinner(m.palette, "")
	if strings.Count(text, "Thinking:") != 1 {
		t.Fatal(text)
	}
	if strings.Index(text, "Need to inspect") > strings.Index(text, "> read_file") || strings.Index(text, "found") > strings.Index(text, "The answer is here.") {
		t.Fatal(text)
	}
	if !strings.Contains(m.completedLines(), "</thinking>") {
		t.Fatal("reasoning was not closed before tools")
	}
}

func TestChatWrapsAllRowsAndReflowsOnResize(t *testing.T) {
	m := streamTestModel(t)
	m.chat.addUser(strings.Repeat("long user text ", 30))
	m.chat.addAssistant(strings.Repeat("long assistant text ", 30))
	m.chat.addSystem(strings.Repeat("unbroken", 100))
	m.refreshTranscript()
	for _, width := range []int{100, 42, 76} {
		next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
		m = next.(Model)
		text := m.chat.renderWithSpinner(m.palette, "")
		for _, line := range strings.Split(text, "\n") {
			if lipgloss.Width(line) > width {
				t.Fatalf("width %d: %d %q", width, lipgloss.Width(line), line)
			}
		}
	}
}

func TestDashboardLayoutChangeKeepsFollowingAndManualScroll(t *testing.T) {
	m := streamTestModel(t)
	m.dashboardFn = func() DashboardSnapshot {
		return DashboardSnapshot{Project: "Project", Goal: goal.ProgressSnapshot{Title: "Fix it", Done: 1, Total: 3}}
	}
	m.chat.addAssistant(strings.Repeat("history\n", 120))
	m.refreshTranscript()
	m.viewport.GotoBottom()
	m.busy = false
	m.resizeViewport()
	m.busy = true
	m.resizeViewport()
	if !m.viewport.AtBottom() {
		t.Fatal("adding busy row stopped follow")
	}
	m.chat.addAssistant("latest message")
	m.refreshTranscript()
	if !m.viewport.AtBottom() || !strings.Contains(m.viewport.View(), "latest message") {
		t.Fatal("latest message not followed")
	}
	m.viewport.GotoTop()
	m.resizeViewport()
	m.chat.addAssistant("more text")
	m.refreshTranscript()
	if m.viewport.YOffset != 0 {
		t.Fatal("manual history position stolen")
	}
}

func TestDashboardFitsWideNarrowAndShortTerminals(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {90, 30}, {80, 25}, {42, 18}} {
		m := New(Options{NoColor: true, Language: "pl", DashboardFn: func() DashboardSnapshot {
			return DashboardSnapshot{Project: "Universal_Service_OS", Directory: "C:/Projects/Universal_Service_OS", SessionTokens: 1200, DailyTokens: 5600, Goal: goal.ProgressSnapshot{Title: strings.Repeat("Długi cel ", 20), Done: 2, Total: 3}}
		}})
		next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = next.(Model)
		m.runtimeContext = contextSnapshot{Used: 1200, Window: 100000, CompactAt: 94, Cached: 127, Evaluated: 1073, HasCache: true, Requests: 4}
		view := m.renderDashboard()
		if lipgloss.Height(view) != m.dashboardHeight() {
			t.Fatalf("dashboard rows: got=%d want=%d", lipgloss.Height(view), m.dashboardHeight())
		}
		for _, row := range strings.Split(m.View(), "\n") {
			if lipgloss.Width(row) > size[0] {
				t.Fatalf("full view overflow size=%v width=%d: %q", size, lipgloss.Width(row), row)
			}
		}
		for _, line := range strings.Split(view, "\n") {
			if lipgloss.Width(line) > size[0] {
				t.Fatalf("size=%v row=%d: %q", size, lipgloss.Width(line), line)
			}
		}
		if strings.Contains(view, "model:") || strings.Contains(view, "effort:") || strings.Contains(view, "credits:") {
			t.Fatal(view)
		}
		if !strings.Contains(view, "2/3") {
			t.Fatal("goal progress missing")
		}
	}
}

func TestToolExpansionUsesOriginalOutputAndUnwrapsProcessJSON(t *testing.T) {
	m := streamTestModel(t)
	m.chat.addToolResult("ctx_execute", "{\"stdout\":\"one\\ntwo\\nthree\\nfour\\nfive\\nsix\",\"stderr\":\"\",\"exit_code\":0}", "")
	m.refreshTranscript()
	if strings.Contains(m.viewport.View(), "six") {
		t.Fatal("fixture not collapsed")
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("E")})
	m = next.(Model)
	if !strings.Contains(m.viewport.View(), "six") || strings.Contains(m.viewport.View(), "stdout") {
		t.Fatal(m.viewport.View())
	}
}
