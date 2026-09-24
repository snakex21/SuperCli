package tui

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"supercli/internal/agent"
)

func TestAskQueuesConcurrentWorkersAndSkipsExpired(t *testing.T) {
	m := New(Options{})
	first, second, third := sampleRequest(), sampleRequest(), sampleRequest()
	first.ID, second.ID, third.ID = "first", "second", "third"
	second.Question = "Second worker?"
	third.Question = "Third worker?"
	expired := make(chan struct{})
	second.Done = expired
	next, _ := m.beginAsk(first)
	m = next.(Model)
	next, _ = m.beginAsk(second)
	m = next.(Model)
	next, _ = m.beginAsk(third)
	m = next.(Model)
	if m.pendingAsk.ID != "first" || len(m.askQueue) != 2 {
		t.Fatal("parallel question replaced active question")
	}
	select {
	case <-first.Respond:
		t.Fatal("first question was cancelled")
	default:
	}
	close(expired)
	next, _ = m.handleAskKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.pendingAsk == nil || m.pendingAsk.ID != "third" {
		t.Fatal("did not advance past expired question")
	}
}

func TestAskExpiryDismissesAndCtrlCCancelsQuestionOnly(t *testing.T) {
	m := New(Options{})
	req := sampleRequest()
	req.ID = "active"
	next, _ := m.beginAsk(req)
	m = next.(Model)
	next, _ = m.Update(askClosedMsg("active"))
	m = next.(Model)
	if m.pendingAsk != nil || m.mode != modeNormal {
		t.Fatal("expired question stayed visible")
	}
	next, _ = m.beginAsk(req)
	m = next.(Model)
	m.busy = true
	next, _ = m.handleCtrlC()
	m = next.(Model)
	if !m.busy || m.pendingAsk != nil {
		t.Fatal("Ctrl+C cancelled the entire agent instead of its question")
	}
}

func TestQuestionPanelFitsUnicodeAndSmallTerminals(t *testing.T) {
	req := sampleRequest()
	req.Question = "Zażółć gęślą jaźń — wybierz sposób działania 界面."
	req.Options[0].Description = strings.Repeat("Długaść界 ", 20)
	a := pendingAskFrom(req)
	for _, size := range [][2]int{{32, 12}, {60, 16}, {100, 24}, {12, 8}} {
		for cursor := range a.Options {
			a.cursor = cursor
			view := renderAskView(a, size[0], size[1], "pl")
			if !utf8.ValidString(view) {
				t.Fatalf("invalid UTF-8 at %v", size)
			}
			lines := strings.Split(view, "\n")
			if len(lines) > size[1] {
				t.Fatalf("height %d > %d", len(lines), size[1])
			}
			for _, line := range lines {
				if ansi.StringWidth(line) > size[0] {
					t.Fatalf("overflow at %v: %q", size, line)
				}
			}
		}
	}
}

func TestStreamingPreservesScrollPosition(t *testing.T) {
	m := New(Options{})
	m.viewport.Height = 5
	for i := 0; i < 30; i++ {
		m.chat.addSystem(strings.Repeat("history ", 10))
	}
	m.refreshTranscript()
	m.viewport.GotoTop()
	next, _ := m.handleAgentEvent(agent.MessageEvent{Text: "new streaming text"})
	m = next.(Model)
	if m.viewport.YOffset != 0 {
		t.Fatal("streaming pulled reader away from history")
	}
	m.viewport.GotoBottom()
	next, _ = m.handleAgentEvent(agent.MessageEvent{Text: strings.Repeat("\nnew line", 20)})
	m = next.(Model)
	if !m.viewport.AtBottom() {
		t.Fatal("streaming stopped following at the bottom")
	}
}

func TestParallelToolResultsUseMatchingName(t *testing.T) {
	m := New(Options{NoColor: true})
	next, _ := m.handleAgentEvent(agent.ToolCallEvent{ID: "a", Name: "read_lines", Args: "{}"})
	m = next.(Model)
	next, _ = m.handleAgentEvent(agent.ToolCallEvent{ID: "b", Name: "search_code", Args: "{}"})
	m = next.(Model)
	next, _ = m.handleAgentEvent(agent.ToolResultEvent{ID: "a", Output: "read-result"})
	m = next.(Model)
	if strings.Contains(m.completedLines(), "search_code: read-result") {
		t.Fatal("result attributed to latest tool instead of matching call")
	}
	if _, ok := m.toolNames["a"]; ok {
		t.Fatal("completed call retained")
	}
	if m.toolNames["b"] != "search_code" {
		t.Fatal("other pending call removed")
	}
}

func TestWorkerOverviewKeepsIdentityAndBounds(t *testing.T) {
	m := New(Options{NoColor: true, Language: "pl"})
	m.width, m.height = 75, 28
	events := []agent.WorkerProgressEvent{
		{TaskID: "worker-2", Agent: "code", Kind: "started", Prompt: "Drugie zadanie"},
		{TaskID: "worker-1", Agent: "code", Kind: "started", Prompt: "Pierwsze zadanie"},
		{TaskID: "worker-1", Kind: "finished", Status: "done"},
		{TaskID: "worker-1", Agent: "code", Kind: "started", Run: 2, Prompt: "Kontynuuj"},
		{TaskID: "worker-1", Kind: "tool_call", Tool: "read_lines"},
	}
	for _, event := range events {
		m.updateWorkerView(event)
	}
	view := strings.Join(m.workerPanelLines(), "\n")
	if len(m.workerViews) != 2 || !strings.Contains(view, "Workerzy · pracuje: 2 / 2") ||
		!strings.Contains(view, "Worker 1 · code · pracuje · read_lines") {
		t.Fatalf("bad worker overview: %s", view)
	}
	if strings.Index(view, "Worker 1") > strings.Index(view, "Worker 2") {
		t.Fatal("worker order changed with event arrival order")
	}
	for i := 3; i < 14; i++ {
		m.updateWorkerView(agent.WorkerProgressEvent{TaskID: fmt.Sprintf("worker-%d", i), Kind: "finished", Status: "done"})
	}
	m.width, m.height = 32, 20
	lines := m.workerPanelLines()
	if len(lines) > 5 || !strings.Contains(strings.Join(lines, "\n"), "/workers") {
		t.Fatalf("panel not bounded: %v", lines)
	}
	for _, line := range lines {
		if ansi.StringWidth(line) > m.width {
			t.Fatalf("worker line too wide: %q", line)
		}
	}
	m.height = 12
	if len(m.workerPanelLines()) != 0 {
		t.Fatal("panel took over tiny terminal")
	}
}
