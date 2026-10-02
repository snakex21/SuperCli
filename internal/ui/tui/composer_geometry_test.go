package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func BenchmarkTUIComposerKeyUpdate(b *testing.B) {
	m := New(Options{Home: b.TempDir(), NoColor: true, Language: "en"})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 36})
	m = next.(Model)
	m.dashboardFn = func() DashboardSnapshot { return DashboardSnapshot{Project: "fixture", Directory: "./fixture"} }
	m.input.SetValue("A short unsent draft")
	m.input.CursorEnd()
	m.syncInputHeight()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
		m = next.(Model)
		next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyBackspace})
		m = next.(Model)
	}
}

func TestComposerTypingKeepsStableViewportGeometry(t *testing.T) {
	for _, busy := range []bool{false, true} {
		m := newComposerGeometryTestModel(t)
		snapshotReads := 0
		m.dashboardFn = func() DashboardSnapshot { snapshotReads++; return DashboardSnapshot{Project: "fixture"} }
		m.input.SetValue("draft")
		m.input.CursorEnd()
		m.syncInputHeight()
		height := m.viewport.Height
		snapshotReads = 0
		var next tea.Model
		key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}
		if busy {
			next, _ = m.handleBusyInput(key)
		} else {
			next, _ = m.handleKey(key)
		}
		updated := next.(Model)
		if updated.input.Value() != "drafta" {
			t.Fatalf("busy=%t input=%q", busy, updated.input.Value())
		}
		if updated.viewport.Height != height {
			t.Fatalf("busy=%t height changed %d -> %d", busy, height, updated.viewport.Height)
		}
		if snapshotReads != 0 {
			t.Fatalf("busy=%t unchanged input caused %d dashboard layout reads", busy, snapshotReads)
		}
	}
}

func TestComposerGeometryStillTracksNewlinesAndAttachmentTransitions(t *testing.T) {
	m := newComposerGeometryTestModel(t)
	m.input.SetValue("line one")
	m.syncInputHeight()
	height := m.viewport.Height
	m.input.SetValue("line one\nline two")
	m.syncTypedInputHeight()
	if m.input.Height() != 2 || m.viewport.Height != height-1 {
		t.Fatalf("newline heights input=%d viewport=%d want viewport=%d", m.input.Height(), m.viewport.Height, height-1)
	}
	m.input.Reset()
	m.syncTypedInputHeight()
	if m.input.Height() != 1 || m.viewport.Height != height {
		t.Fatalf("reset did not restore geometry")
	}
	m.pendingAttachments = []string{"./fixture.png"}
	m.syncInputHeight()
	if m.viewport.Height != height-1 {
		t.Fatalf("same input height must still track attachment chrome")
	}
	m.pendingAttachments = nil
	m.syncInputHeight()
	if m.viewport.Height != height {
		t.Fatalf("removing attachment must restore viewport")
	}
}

func TestComposerAutocompleteStillResizesOnOpenFilterAndClose(t *testing.T) {
	m := newComposerGeometryTestModel(t)
	m.syncInputHeight()
	height := m.viewport.Height
	m.input.SetValue("/")
	m.updateAutocompleteState()
	if m.autocomp.kind != autocompSlash || m.viewport.Height != m.viewportHeight() || m.viewport.Height >= height {
		t.Fatalf("opening slash palette did not reserve its rows: kind=%v height=%d", m.autocomp.kind, m.viewport.Height)
	}
	m.input.SetValue("/reasoning")
	m.updateAutocompleteState()
	if m.autocomp.kind != autocompSlash || m.viewport.Height != m.viewportHeight() {
		t.Fatalf("filter did not adjust geometry")
	}
	m.input.SetValue("plain text")
	m.updateAutocompleteState()
	if m.autocomp.kind != autocompNone || m.viewport.Height != height {
		t.Fatalf("closing palette did not restore viewport")
	}
}

func newComposerGeometryTestModel(t *testing.T) Model {
	m := newTestModel(t)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	return next.(Model)
}

func TestComposerEscapeClearsMultilineDraftAndRestoresViewport(t *testing.T) {
	m := newComposerGeometryTestModel(t)
	height := m.viewport.Height
	m.input.SetValue("line one\nline two\nline three")
	m.syncInputHeight()
	if m.input.Height() != 3 {
		t.Fatal("fixture must start multiline")
	}
	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	cleared := next.(Model)
	if cleared.input.Value() != "" || cleared.input.Height() != 1 || cleared.viewport.Height != height {
		t.Fatalf("Escape left stale input geometry: text=%q input=%d viewport=%d want=%d", cleared.input.Value(), cleared.input.Height(), cleared.viewport.Height, height)
	}
}
