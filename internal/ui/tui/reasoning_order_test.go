package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/agent"
)

func reasoningOrderModel() Model {
	m := New(Options{Language: "en", NoColor: true})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 35})
	return next.(Model)
}

func TestLateNativeReasoningPrecedesAnswerAndCompletes(t *testing.T) {
	for _, events := range [][]agent.Event{
		{agent.MessageEvent{Text: "Visible answer."}, agent.ReasoningEvent{Text: "Delayed thought."}},
		{agent.ReasoningEvent{Text: "First thought."}, agent.MessageEvent{Text: "Visible answer."}, agent.ReasoningEvent{Text: " Delayed thought."}},
	} {
		m := reasoningOrderModel()
		for _, event := range events {
			next, _ := m.handleAgentEvent(event)
			m = next.(Model)
		}
		view := m.View()
		if thought, answer := strings.Index(view, "thought."), strings.Index(view, "Visible answer."); thought < 0 || answer < 0 || thought >= answer {
			t.Fatalf("native reasoning must precede prose: %q", view)
		}
		next, _ := m.handleAgentEvent(agent.MessageEvent{Text: " More prose."})
		next, _ = next.(Model).handleAgentEvent(agent.DoneEvent{})
		m = next.(Model)
		if m.current != "" || m.reasoningOpen || m.nativeReasoningEnd != 0 {
			t.Fatal("completion retained active native reasoning state")
		}
		completed := m.chat.lastAssistant()
		if strings.Count(completed, "<thinking>") != 1 || !strings.HasSuffix(completed, "Visible answer. More prose.") {
			t.Fatalf("completion lost/reclassified prose or created multiple native phases: %q", completed)
		}
		if view := m.View(); strings.Index(view, "thought.") >= strings.Index(view, "Visible answer.") {
			t.Fatalf("completed view reversed reasoning/prose: %q", view)
		}
	}
}

func TestLateNativeReasoningCopyAndNextSegmentStayIndependent(t *testing.T) {
	m := reasoningOrderModel()
	next, _ := m.handleAgentEvent(agent.MessageEvent{Text: "Original answer."})
	m = next.(Model)
	copied := m
	next, _ = copied.handleAgentEvent(agent.ReasoningEvent{Text: "Copied thought."})
	copied = next.(Model)
	if m.current != "Original answer." || m.chat.current != "Original answer." {
		t.Fatal("late reasoning changed a copied model's original stream")
	}
	next, _ = copied.handleAgentEvent(agent.MessageEvent{Text: " Copied tail."})
	copied = next.(Model)
	if !strings.HasSuffix(copied.current, "Original answer. Copied tail.") || strings.Contains(m.current, "Copied") {
		t.Fatal("the splice reused the old model's append buffer")
	}
	copied.flushCurrent()
	next, _ = copied.handleAgentEvent(agent.ReasoningEvent{Text: "Next thought."})
	next, _ = next.(Model).handleAgentEvent(agent.MessageEvent{Text: "Next answer."})
	copied = next.(Model)
	if copied.current != "<thinking>Next thought.</thinking>\nNext answer." {
		t.Fatalf("new segment reused the previous native boundary: %q", copied.current)
	}
}
