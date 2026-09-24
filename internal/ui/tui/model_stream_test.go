package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/agent"
)

func streamTestModel(t *testing.T) Model {
	t.Helper()
	m := New(Options{Home: t.TempDir(), NoColor: true})
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 36})
	m = out.(Model)
	out, _ = m.Update(runStartMsg{ch: make(chan agent.Event)})
	return out.(Model)
}

func TestStreamBufferKeepsCopiedModelsAndSnapshotsIndependent(t *testing.T) {
	var original Model
	original.appendStreamText("Zażółć ")
	snapshot := original.current
	later := original
	later.appendStreamText("😀")
	// Advancing an older value must not append to the newer model's tail.
	original.appendStreamText("中文")
	if snapshot != "Zażółć " || later.current != "Zażółć 😀" || original.current != "Zażółć 中文" {
		t.Fatalf("snapshot=%q later=%q original=%q", snapshot, later.current, original.current)
	}
	later.resetCurrent()
	later.appendStreamText("new reply")
	if snapshot != "Zażółć " || original.current != "Zażółć 中文" || later.chat.lastAssistant() != "new reply" {
		t.Fatal("reset changed another snapshot")
	}
	// A seeded/replaced current string (e.g. restored UI state) is respected.
	original.current = "replacement"
	original.appendStreamText(" tail")
	if original.current != "replacement tail" {
		t.Fatal(original.current)
	}
}

func TestStreamFrameRefreshesDirtyEventsAndSpinner(t *testing.T) {
	m := streamTestModel(t)
	m.spinner = spinner.New(spinner.WithSpinner(spinner.Spinner{Frames: []string{"spin-A", "spin-B"}, FPS: time.Second}))
	out, _ := m.Update(runEventMsg{ev: agent.MessageEvent{Text: "answer"}})
	m = out.(Model)
	if !strings.Contains(m.View(), "answer") {
		t.Fatal("first chunk is not visible immediately")
	}
	before := m.viewport.View()
	out, cmd := m.Update(streamFlushMsg{})
	m = out.(Model)
	if cmd == nil || m.viewport.View() != before {
		t.Fatal("unchanged frame changed content or stopped timer")
	}
	// Tool/notice rows have no new assistant text, but must still be painted.
	m.appendLine("tool completed")
	out, _ = m.Update(streamFlushMsg{})
	m = out.(Model)
	if !strings.Contains(m.viewport.View(), "tool completed") {
		t.Fatal("dirty history not painted")
	}
	first := m.renderedSpinner
	out, _ = m.Update(m.spinner.Tick())
	m = out.(Model)
	if m.streamSpinner() == first {
		t.Fatal("fixture did not advance spinner")
	}
	out, _ = m.Update(streamFlushMsg{})
	m = out.(Model)
	if m.renderedSpinner == first || !strings.Contains(m.viewport.View(), "spin-B") {
		t.Fatal("spinner did not repaint")
	}
	// Ending a run must remove the busy indicator, even without more text.
	out, _ = m.Update(runEndMsg{})
	m = out.(Model)
	if m.renderedSpinner != "" || strings.Contains(m.viewport.View(), "spin-B") {
		t.Fatal("busy spinner survived run end")
	}
}

func TestStreamFramesPreserveScrollAndAllText(t *testing.T) {
	m := streamTestModel(t)
	m.chat.addAssistant(strings.Repeat("older line\n", 100))
	m.refreshTranscript()
	m.viewport.GotoTop()
	offset := m.viewport.YOffset
	for _, chunk := range []string{"first ", "**bold** ", "last"} {
		out, _ := m.Update(runEventMsg{ev: agent.MessageEvent{Text: chunk}})
		m = out.(Model)
		out, _ = m.Update(streamFlushMsg{})
		m = out.(Model)
		if m.viewport.YOffset != offset {
			t.Fatal("stream stole scroll position")
		}
	}
	if m.current != "first **bold** last" || m.chat.lastAssistant() != m.current {
		t.Fatal("stream or copy text differs")
	}
	// Explicit layout and thinking changes still force a fresh render.
	out, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 25})
	m = out.(Model)
	if m.viewport.Width != 80 {
		t.Fatal("resize did not apply")
	}
}

func TestStreamBufferReleasedAtCompletionAndRestart(t *testing.T) {
	for _, terminal := range []agent.Event{agent.DoneEvent{}, agent.ErrorEvent{Err: errors.New("interrupted")}} {
		m := streamTestModel(t)
		for _, ev := range []agent.Event{agent.ReasoningEvent{Text: "plan"}, agent.MessageEvent{Text: "reply"}} {
			out, _ := m.Update(runEventMsg{ev: ev})
			m = out.(Model)
		}
		snapshot := m.current
		if snapshot != "<thinking>plan</thinking>\nreply" {
			t.Fatalf("changed transcript: %q", snapshot)
		}
		// Terminal events do not finish a run until persistence closes its stream.
		ch := make(chan agent.Event)
		close(ch)
		m.eventCh = ch
		out, cmd := m.Update(runEventMsg{ev: terminal})
		m = out.(Model)
		if m.current != "" || m.chat.current != "" || m.currentBuffer != nil {
			t.Fatal("completed buffer still attached")
		}
		if !strings.Contains(m.completedLines(), snapshot) {
			t.Fatal("completion lost content")
		}
		if cmd == nil {
			t.Fatal("missing run-end command")
		}
		out, _ = m.Update(cmd())
		m = out.(Model)
		out, _ = m.Update(runStartMsg{ch: make(chan agent.Event)})
		m = out.(Model)
		out, _ = m.Update(runEventMsg{ev: agent.MessageEvent{Text: "next"}})
		m = out.(Model)
		if m.current != "next" || snapshot != "<thinking>plan</thinking>\nreply" {
			t.Fatal("next run corrupted earlier text")
		}
	}
}
