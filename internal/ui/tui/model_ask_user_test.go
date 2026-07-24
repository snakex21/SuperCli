package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/agent"
	"supercli/internal/tools"
)

// pendingAskFrom builds a pendingAsk in the state beginAsk would
// have produced. Used by tests that want to skip the
// AskRequestMsg path.
func pendingAskFrom(req tools.AskRequest) *pendingAsk {
	return &pendingAsk{
		Question:    req.Question,
		Header:      req.Header,
		Options:     req.Options,
		MultiSelect: req.MultiSelect,
		cursor:      0,
		toggled:     make(map[int]bool),
		respond:     req.Respond,
	}
}

func sampleRequest() tools.AskRequest {
	return tools.AskRequest{
		ID:       "test-1",
		Question: "Which database?",
		Header:   "DB",
		Options: []tools.AskOption{
			{Label: "SQLite", Description: "no CGO"},
			{Label: "Postgres", Description: "requires lib/pq"},
			{Label: "DuckDB", Description: "embedded OLAP"},
		},
		Respond: make(chan tools.AskAnswer, 1),
	}
}

func TestBeginAsk_SwitchesMode(t *testing.T) {
	m := New(Options{Home: "/x"})
	out, _ := m.Update(askRequestMsg{req: sampleRequest()})
	mm := out.(Model)
	if mm.mode != modeAsking {
		t.Fatalf("mode = %d, want modeAsking", mm.mode)
	}
	if mm.pendingAsk == nil {
		t.Fatal("pendingAsk is nil")
	}
	if mm.pendingAsk.Question != "Which database?" {
		t.Fatalf("Question = %q", mm.pendingAsk.Question)
	}
}

func TestBeginAsk_OverridesPrevious(t *testing.T) {
	m := New(Options{Home: "/x"})
	// First ask
	m.Update(askRequestMsg{req: sampleRequest()})
	// Second ask should cancel the first.
	second := sampleRequest()
	second.Question = "Second?"
	out, _ := m.Update(askRequestMsg{req: second})
	mm := out.(Model)
	if mm.pendingAsk.Question != "Second?" {
		t.Fatalf("Question = %q, want Second?", mm.pendingAsk.Question)
	}
}

func TestHandleAskKey_QuickPick1(t *testing.T) {
	req := sampleRequest()
	m := New(Options{Home: "/x"})
	m.mode = modeAsking
	m.pendingAsk = pendingAskFrom(req)
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	mm := out.(Model)
	if mm.mode != modeNormal {
		t.Fatalf("mode = %d, want modeNormal", mm.mode)
	}
	ans := <-req.Respond
	if ans.Cancelled {
		t.Fatal("should not be cancelled")
	}
	if len(ans.Selected) != 1 || ans.Selected[0] != "SQLite" {
		t.Fatalf("Selected = %v, want [SQLite]", ans.Selected)
	}
}

func TestHandleAskKey_QuickPick2(t *testing.T) {
	req := sampleRequest()
	m := New(Options{Home: "/x"})
	m.mode = modeAsking
	m.pendingAsk = pendingAskFrom(req)
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	mm := out.(Model)
	if mm.mode != modeNormal {
		t.Fatal("mode should return to normal")
	}
	ans := <-req.Respond
	if ans.Selected[0] != "Postgres" {
		t.Fatalf("Selected = %v, want [Postgres]", ans.Selected)
	}
}

func TestHandleAskKey_QuickPickOutOfRange(t *testing.T) {
	req := sampleRequest()
	m := New(Options{Home: "/x"})
	m.mode = modeAsking
	m.pendingAsk = pendingAskFrom(req)
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'9'}})
	mm := out.(Model)
	if mm.mode != modeAsking {
		t.Fatal("should still be asking (out of range)")
	}
}

func TestHandleAskKey_ArrowDown(t *testing.T) {
	req := sampleRequest()
	m := New(Options{Home: "/x"})
	m.mode = modeAsking
	m.pendingAsk = pendingAskFrom(req)
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm := out.(Model)
	if mm.pendingAsk.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", mm.pendingAsk.cursor)
	}
}

func TestHandleAskKey_ArrowUpAtTop(t *testing.T) {
	req := sampleRequest()
	m := New(Options{Home: "/x"})
	m.mode = modeAsking
	m.pendingAsk = pendingAskFrom(req)
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	mm := out.(Model)
	if mm.pendingAsk.cursor != 0 {
		t.Fatalf("cursor = %d, want 0", mm.pendingAsk.cursor)
	}
}

func TestHandleAskKey_EnterSingleSelect(t *testing.T) {
	req := sampleRequest()
	m := New(Options{Home: "/x"})
	m.mode = modeAsking
	m.pendingAsk = pendingAskFrom(req)
	m.pendingAsk.cursor = 1
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := out.(Model)
	if mm.mode != modeNormal {
		t.Fatal("mode should be normal")
	}
	ans := <-req.Respond
	if len(ans.Selected) != 1 || ans.Selected[0] != "Postgres" {
		t.Fatalf("Selected = %v", ans.Selected)
	}
}

func TestHandleAskKey_EscCancels(t *testing.T) {
	req := sampleRequest()
	m := New(Options{Home: "/x"})
	m.mode = modeAsking
	m.pendingAsk = pendingAskFrom(req)
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	mm := out.(Model)
	if mm.mode != modeNormal {
		t.Fatal("mode should be normal")
	}
	ans := <-req.Respond
	if !ans.Cancelled {
		t.Fatal("should be cancelled")
	}
}

func TestHandleAskKey_MultiSelectToggle(t *testing.T) {
	req := sampleRequest()
	req.MultiSelect = true
	m := New(Options{Home: "/x"})
	m.mode = modeAsking
	m.pendingAsk = pendingAskFrom(req)
	// Press 1 → toggle on.
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	mm := out.(Model)
	if !mm.pendingAsk.toggled[0] {
		t.Fatal("option 0 should be toggled on")
	}
	// Press 1 again → toggle off.
	out, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	mm = out.(Model)
	if mm.pendingAsk.toggled[0] {
		t.Fatal("option 0 should be toggled off")
	}
}

func TestHandleAskKey_MultiSelectEnter(t *testing.T) {
	req := sampleRequest()
	req.MultiSelect = true
	m := New(Options{Home: "/x"})
	m.mode = modeAsking
	m.pendingAsk = pendingAskFrom(req)
	m.pendingAsk.toggled[0] = true
	m.pendingAsk.toggled[2] = true
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := out.(Model)
	if mm.mode != modeNormal {
		t.Fatal("mode should be normal")
	}
	ans := <-req.Respond
	if !ans.MultiSelect {
		t.Fatal("MultiSelect should be true")
	}
	if len(ans.Selected) != 2 {
		t.Fatalf("Selected = %v, want 2 entries", ans.Selected)
	}
}

func TestHandleAskKey_SpaceTogglesMultiSelect(t *testing.T) {
	req := sampleRequest()
	req.MultiSelect = true
	m := New(Options{Home: "/x"})
	m.mode = modeAsking
	m.pendingAsk = pendingAskFrom(req)
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	mm := out.(Model)
	if !mm.pendingAsk.toggled[0] {
		t.Fatal("space should toggle option 0")
	}
}

func TestHandleAskKey_SpaceIgnoredSingleSelect(t *testing.T) {
	req := sampleRequest()
	req.MultiSelect = false
	m := New(Options{Home: "/x"})
	m.mode = modeAsking
	m.pendingAsk = pendingAskFrom(req)
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	mm := out.(Model)
	if mm.mode != modeAsking {
		t.Fatal("should still be asking")
	}
	if mm.pendingAsk.toggled[0] {
		t.Fatal("space should not toggle in single-select")
	}
}

func TestView_RendersAskUI(t *testing.T) {
	req := sampleRequest()
	m := New(Options{Home: "/x"})
	m.width = 100
	m.height = 30
	m.mode = modeAsking
	m.pendingAsk = pendingAskFrom(req)
	v := m.View()
	if !strings.Contains(v, "Which database?") {
		t.Fatalf("view missing question: %q", v)
	}
	if !strings.Contains(v, "SQLite") || !strings.Contains(v, "Postgres") {
		t.Fatalf("view missing options: %q", v)
	}
	if !strings.Contains(v, "[DB]") {
		t.Fatalf("view missing header: %q", v)
	}
	if !strings.Contains(v, "no CGO") {
		t.Fatalf("view missing description: %q", v)
	}
}

func TestView_AskUIMultilineWraps(t *testing.T) {
	req := sampleRequest()
	req.Question = "This is a very long question that should definitely be wrapped onto multiple lines when displayed in the ask UI to look readable."
	m := New(Options{Home: "/x"})
	m.width = 100
	m.height = 30
	m.mode = modeAsking
	m.pendingAsk = pendingAskFrom(req)
	v := m.View()
	if !strings.Contains(v, "wrapped") {
		t.Fatalf("view missing wrapped text: %q", v)
	}
}

func TestSafeRespond_NonBlocking(t *testing.T) {
	ch := make(chan tools.AskAnswer, 1)
	safeRespond(ch, tools.AskAnswer{Selected: []string{"x"}})
	ans := <-ch
	if ans.Selected[0] != "x" {
		t.Fatalf("got %v", ans.Selected)
	}
}

func TestSafeRespond_IgnoresFullChannel(t *testing.T) {
	// Buffered channel already full → safeRespond must not block.
	ch := make(chan tools.AskAnswer, 1)
	ch <- tools.AskAnswer{Cancelled: true}
	done := make(chan struct{})
	go func() {
		safeRespond(ch, tools.AskAnswer{Selected: []string{"x"}})
		close(done)
	}()
	select {
	case <-done:
		// good: did not block
	case <-time.After(100 * time.Millisecond):
		t.Fatal("safeRespond blocked on full channel")
	}
}

func TestWrap_Basic(t *testing.T) {
	got := wrap("the quick brown fox jumps over the lazy dog", 15)
	if len(got) < 3 {
		t.Fatalf("got %d lines, want >= 3", len(got))
	}
	for _, l := range got {
		if len(l) > 15 {
			t.Errorf("line %q is %d chars, max 15", l, len(l))
		}
	}
}

func TestWrap_Empty(t *testing.T) {
	if got := wrap("", 10); len(got) != 1 || got[0] != "" {
		t.Fatalf("got %v", got)
	}
}

func TestWrap_SingleLongWord(t *testing.T) {
	// A word longer than width is left as a single line.
	got := wrap("supercalifragilisticexpialidocious", 10)
	if len(got) != 1 {
		t.Fatalf("got %d lines, want 1", len(got))
	}
}

func TestEndAsk_ClearsState(t *testing.T) {
	m := New(Options{Home: "/x"})
	m.mode = modeAsking
	m.pendingAsk = pendingAskFrom(sampleRequest())
	m.endAsk()
	if m.mode != modeNormal {
		t.Fatalf("mode = %d, want modeNormal", m.mode)
	}
	if m.pendingAsk != nil {
		t.Fatal("pendingAsk should be nil")
	}
}

func TestAskModeIgnoresOtherInput(t *testing.T) {
	req := sampleRequest()
	m := New(Options{Home: "/x"})
	m.mode = modeAsking
	m.pendingAsk = pendingAskFrom(req)
	// 'q' would normally quit, but in ask mode it should not.
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	mm := out.(Model)
	if mm.quitting {
		t.Fatal("should not quit in ask mode")
	}
}

func TestRenderAskView_VariousWidths(t *testing.T) {
	req := sampleRequest()
	for _, w := range []int{40, 60, 80, 120} {
		m := New(Options{Home: "/x"})
		m.width = w
		m.height = 30
		m.mode = modeAsking
		m.pendingAsk = pendingAskFrom(req)
		v := m.View()
		if v == "" {
			t.Errorf("width %d: empty view", w)
		}
	}
}

// keeps agent import alive in case future tests need it
var _ = agent.DoneEvent{}
