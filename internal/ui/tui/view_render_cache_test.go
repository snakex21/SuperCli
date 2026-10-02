package tui

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"supercli/internal/agent"
	"supercli/internal/storage/goal"
)

func renderCacheTestModel() Model {
	m := New(Options{NoColor: true, Language: "en", Version: "test", DashboardFn: func() DashboardSnapshot {
		return DashboardSnapshot{Project: "workspace", Directory: "portable", SessionTokens: 123, DailyTokens: 456, Goal: goal.ProgressSnapshot{Title: "Inspect the result", Done: 1, Total: 3}}
	}})
	m.width, m.height = 120, 36
	m.viewport.Width = m.width
	m.input.SetWidth(m.width)
	m.chat.width = m.width
	m.chat.addAssistant(strings.Repeat("prior answer line\n", 80))
	m.busy = true
	m.eventCh = make(chan agent.Event)
	m.refreshTranscript()
	return m
}

func assertFreshRender(t *testing.T, m Model) string {
	t.Helper()
	got := m.View()
	fresh := m
	fresh.renderCache = nil
	want := fresh.View()
	if got != want {
		t.Fatalf("cached View differs from fresh View\ncached:\n%s\nfresh:\n%s", got, want)
	}
	return got
}

func TestRenderCacheMatchesFreshViewAfterUIEvents(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Model)
	}{
		{"resize", func(m *Model) { next, _ := m.Update(tea.WindowSizeMsg{Width: 42, Height: 24}); *m = next.(Model) }},
		{"input", func(m *Model) {
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("draft while streaming")})
			*m = next.(Model)
		}},
		{"keyboard scroll", func(m *Model) { next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp}); *m = next.(Model) }},
		{"mouse scroll", func(m *Model) {
			next, _ := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp, X: 4, Y: 3})
			*m = next.(Model)
		}},
		{"menu open and close", func(m *Model) {
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
			*m = next.(Model)
			assertFreshRender(t, *m)
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
			*m = next.(Model)
		}},
		{"spinner", func(m *Model) {
			m.spinner = spinner.New(spinner.WithSpinner(spinner.Spinner{Frames: []string{"spin-A", "spin-B"}, FPS: time.Second}))
			_ = m.View()
			next, _ := m.Update(m.spinner.Tick())
			*m = next.(Model)
		}},
		{"worker", func(m *Model) {
			m.updateWorkerView(agent.WorkerProgressEvent{TaskID: "worker-1", Agent: "Inspector", Kind: "tool_call", Tool: "read_file"})
			m.resizeViewport()
		}},
		{"notice", func(m *Model) { m.setStatus("a new status notice", true) }},
		{"status refresh", func(m *Model) { next, _ := m.Update(statusRefreshMsg{}); *m = next.(Model) }},
		{"run ended", func(m *Model) { next, _ := m.Update(runEndMsg{}); *m = next.(Model) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := renderCacheTestModel()
			assertFreshRender(t, m)
			tc.change(&m)
			assertFreshRender(t, m)
		})
	}
}

func TestRenderCacheMutatingHelpersAndCopiesStayFresh(t *testing.T) {
	original := renderCacheTestModel()
	original.input.SetValue("original draft")
	before := assertFreshRender(t, original)
	copyModel := original
	copyModel.width, copyModel.height = 66, 22
	copyModel.input.SetWidth(copyModel.width)
	copyModel.input.SetValue("separate draft\nsecond line")
	copyModel.input.Blur()
	copyModel.runtimeContext = contextSnapshot{Used: 500, Window: 2000, CompactAt: 94, Cached: 20, Evaluated: 100, HasCache: true, Requests: 7}
	copyModel.language = "pl"
	copyModel.viewport.SetContent("new viewport supplied by helper")
	copyModel.viewport.GotoTop()
	copyModel.pendingAttachments = []string{"picture.png"}
	copyModel.workerViews = []workerView{{id: "worker-2", agent: "Editor", status: "done", activity: "finished"}}
	copyModel.setStatus("copied model notice", true)
	copyModel.autocomp = autocomplete{kind: autocompSlash, items: []autocompleteItem{{Label: "/help", Desc: "help", Value: "/help "}}}
	for i := 0; i < 3; i++ {
		assertFreshRender(t, copyModel)
		if got := assertFreshRender(t, original); got != before {
			t.Fatal("rendering a changed copy altered the original frame")
		}
	}
	original.input.Cursor.Blink = !original.input.Cursor.Blink
	original.input.SetCursor(3)
	assertFreshRender(t, original)
	original.input.SetValue("edited outside Update")
	if got := assertFreshRender(t, original); !strings.Contains(got, "edited outside Update") {
		t.Fatal("input helper retained stale border contents")
	}
	original.chat.addAssistant("latest tool result")
	original.refreshTranscript()
	assertFreshRender(t, original)
}

func TestRenderCacheReadsLiveDashboardAndStatusCallbacks(t *testing.T) {
	snapshot := DashboardSnapshot{Project: "first", SessionTokens: 1, DailyTokens: 2}
	calls := 0
	m := renderCacheTestModel()
	m.dashboardFn = func() DashboardSnapshot { calls++; return snapshot }
	assertFreshRender(t, m)
	before := calls
	_ = m.View()
	_ = m.View()
	if calls != before+2 {
		t.Fatal("dashboard callback was hidden by the cache")
	}
	snapshots := []DashboardSnapshot{
		{Project: "renamed", Directory: "new directory", SessionTokens: 1200, DailyTokens: 5500, SessionCap: 10000, DailyCap: 50000},
		{Project: "renamed", Limits: "remaining quota", Account: "another account", Orchestrator: true},
		{Project: "renamed", Goal: goal.ProgressSnapshot{Title: "new active goal", Done: 2, Total: 3, Verification: "failed"}},
		{Project: "renamed", Goal: goal.ProgressSnapshot{Title: "finished goal", Done: 3, Total: 3, Verification: "passed"}},
	}
	for _, next := range snapshots {
		snapshot = next
		assertFreshRender(t, m)
	}
	// Context counters are cached between turns by the runtime, but their
	// render input must change immediately when a helper replaces the snapshot.
	m.runtimeContext = contextSnapshot{Used: 987, Window: 2000, CompactAt: 95, Cached: 300, Evaluated: 500, HasCache: true, Requests: 9}
	assertFreshRender(t, m)
	m.dashboardFn = nil
	status := "first live status"
	m.statusFn = func() string { return status }
	assertFreshRender(t, m)
	status = "second live status"
	if got := assertFreshRender(t, m); !strings.Contains(got, status) {
		t.Fatal("status callback was stale")
	}
}

func TestRenderCacheStyleAndColorChangesMatchFresh(t *testing.T) {
	renderer := lipgloss.NewRenderer(io.Discard)
	renderer.SetColorProfile(termenv.TrueColor)
	renderer.SetHasDarkBackground(true)
	m := renderCacheTestModel()
	m.palette = NewPalette(renderer)
	m.input.SetValue("styled input")
	before := assertFreshRender(t, m)
	m.palette.InputBorderFocused = m.palette.InputBorderFocused.Border(lipgloss.DoubleBorder()).Padding(1, 2)
	m.palette.Panel = m.palette.Panel.Border(lipgloss.DoubleBorder())
	m.palette.StatusKey = m.palette.StatusKey.Bold(true)
	if got := assertFreshRender(t, m); got == before {
		t.Fatal("style replacement did not change the frame")
	}
	renderer.SetHasDarkBackground(false)
	assertFreshRender(t, m)
	renderer.SetColorProfile(termenv.Ascii)
	assertFreshRender(t, m)
}

func TestRenderCacheStatefulTransformsAlwaysRender(t *testing.T) {
	m := renderCacheTestModel()
	suffix := " first"
	m.palette.InputBorderFocused = m.palette.InputBorderFocused.Transform(func(s string) string { return s + suffix })
	m.palette.Panel = m.palette.Panel.Transform(func(s string) string { return s + suffix })
	first := assertFreshRender(t, m)
	suffix = " second"
	if got := assertFreshRender(t, m); got == first || !strings.Contains(got, suffix) {
		t.Fatal("stateful Style transform was cached")
	}
}

func BenchmarkRenderCacheFullView(b *testing.B) {
	for _, dashboard := range []bool{false, true} {
		name := "plain"
		if dashboard {
			name = "dashboard"
		}
		b.Run(name, func(b *testing.B) {
			for _, enabled := range []bool{false, true} {
				variant := "fresh"
				if enabled {
					variant = "cached"
				}
				b.Run(variant, func(b *testing.B) {
					m := renderCacheTestModel()
					if !dashboard {
						m.dashboardFn = nil
					}
					if !enabled {
						m.renderCache = nil
					}
					_ = m.View()
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						_ = m.View()
					}
				})
			}
		})
	}
}
