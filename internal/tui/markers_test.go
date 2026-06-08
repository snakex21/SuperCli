package tui

import (
	"strings"
	"testing"
)

func TestMarker_Draft_WithSavings(t *testing.T) {
	p := DefaultPalette()
	m := NewMarker(p)
	rendered := m.Draft("haiku", "sonnet", 42, "")
	if !strings.Contains(rendered, "draft") {
		t.Fatalf("missing draft prefix: %q", rendered)
	}
	if !strings.Contains(rendered, "saved 42 tokens") {
		t.Fatalf("missing savings: %q", rendered)
	}
}

func TestMarker_Draft_WithoutSavings(t *testing.T) {
	p := DefaultPalette()
	m := NewMarker(p)
	rendered := m.Draft("haiku", "sonnet", 0, "overridden")
	if !strings.Contains(rendered, "overridden") {
		t.Fatalf("missing decision: %q", rendered)
	}
}

func TestMarker_Council(t *testing.T) {
	p := DefaultPalette()
	m := NewMarker(p)
	rendered := m.Council(3, "groq", "fastest")
	if !strings.Contains(rendered, "3 candidate(s)") {
		t.Fatalf("missing count: %q", rendered)
	}
	if !strings.Contains(rendered, "winner=groq") {
		t.Fatalf("missing winner: %q", rendered)
	}
}

func TestMarker_CouncilAllFailed(t *testing.T) {
	p := DefaultPalette()
	m := NewMarker(p)
	rendered := m.CouncilAllFailed()
	if !strings.Contains(rendered, "all samples failed") {
		t.Fatalf("missing message: %q", rendered)
	}
}

func TestMarker_ContextHid(t *testing.T) {
	p := DefaultPalette()
	m := NewMarker(p)
	rendered := m.ContextHid(5, "budget")
	if !strings.Contains(rendered, "hid 5 message(s)") {
		t.Fatalf("missing count: %q", rendered)
	}
	if !strings.Contains(rendered, "budget") {
		t.Fatalf("missing reason: %q", rendered)
	}
}

func TestMarker_ContextHid_DefaultReason(t *testing.T) {
	p := DefaultPalette()
	m := NewMarker(p)
	rendered := m.ContextHid(2, "")
	if !strings.Contains(rendered, "manual") {
		t.Fatalf("missing default reason: %q", rendered)
	}
}

func TestMarker_Reflection(t *testing.T) {
	p := DefaultPalette()
	m := NewMarker(p)
	rendered := m.Reflection(5)
	if !strings.Contains(rendered, "reflection") {
		t.Fatalf("missing reflection: %q", rendered)
	}
}

func TestMarker_Goal(t *testing.T) {
	p := DefaultPalette()
	m := NewMarker(p)
	rendered := m.Goal(3, 5)
	if !strings.Contains(rendered, "3/5") {
		t.Fatalf("missing progress: %q", rendered)
	}
}

func TestMarker_Done(t *testing.T) {
	p := DefaultPalette()
	m := NewMarker(p)
	rendered := m.Done(120, 340)
	if !strings.Contains(rendered, "120 in") {
		t.Fatalf("missing input count: %q", rendered)
	}
}

func TestMarker_ToolCall(t *testing.T) {
	p := DefaultPalette()
	m := NewMarker(p)
	rendered := m.ToolCall("read_image", `{"path":"x.png"}`)
	if !strings.Contains(rendered, "read_image") {
		t.Fatalf("missing tool name: %q", rendered)
	}
}

func TestMarker_ToolCall_TruncatesArgs(t *testing.T) {
	p := DefaultPalette()
	m := NewMarker(p)
	bigArgs := strings.Repeat("x", 100)
	rendered := m.ToolCall("tool", bigArgs)
	if !strings.Contains(rendered, "...") {
		t.Fatalf("should truncate long args: %q", rendered)
	}
}

func TestMarker_ToolResult(t *testing.T) {
	p := DefaultPalette()
	m := NewMarker(p)
	rendered := m.ToolResult("loaded file", false)
	if !strings.Contains(rendered, "loaded file") {
		t.Fatalf("missing output: %q", rendered)
	}
}

func TestMarker_ToolResult_Error(t *testing.T) {
	p := DefaultPalette()
	m := NewMarker(p)
	rendered := m.ToolResult("not found", true)
	if !strings.Contains(rendered, "error") {
		t.Fatalf("missing error prefix: %q", rendered)
	}
}

func TestMarker_ToolResult_Truncates(t *testing.T) {
	p := DefaultPalette()
	m := NewMarker(p)
	bigOutput := strings.Repeat("a", 300)
	rendered := m.ToolResult(bigOutput, false)
	if !strings.Contains(rendered, "…") {
		t.Fatalf("should truncate long output: %q", rendered)
	}
}

func TestMarker_Running(t *testing.T) {
	p := DefaultPalette()
	m := NewMarker(p)
	rendered := m.Running()
	if !strings.Contains(rendered, "Ctrl+C") {
		t.Fatalf("missing Ctrl+C hint: %q", rendered)
	}
}

func TestMarker_NoAgent(t *testing.T) {
	p := DefaultPalette()
	m := NewMarker(p)
	rendered := m.NoAgent()
	if !strings.Contains(rendered, "no agent wired") {
		t.Fatalf("missing message: %q", rendered)
	}
}
