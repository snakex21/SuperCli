package tui

import (
	"encoding/json"
	"fmt"
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

func TestMarker_ToolCallSummarizesCommonJSON(t *testing.T) {
	p := NoColorPalette()
	m := NewMarker(p)
	rendered := m.ToolCall("read_lines", `{"file":"README.md","from":1,"to":80,"content":"must not leak"}`)
	for _, want := range []string{"README.md", "lines 1–80"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("summary missing %q: %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "must not leak") {
		t.Fatalf("large content field leaked into activity row: %q", rendered)
	}
}

func TestMarker_ToolResultFullIsCompactUntilExpanded(t *testing.T) {
	p := NoColorPalette()
	m := NewMarker(p)
	output := "one\ntwo\nthree\nfour\nfive\nsix"
	compact := m.ToolResultFull("read_lines", output, false)
	if strings.Contains(compact, "│ five") || !strings.Contains(compact, "2 more") {
		t.Fatalf("compact output should show four lines and remainder: %q", compact)
	}
	expanded := m.ToolResultFull("read_lines", output, true)
	if !strings.Contains(expanded, "│ five") || strings.Contains(expanded, "more ·") {
		t.Fatalf("expanded output should contain all lines: %q", expanded)
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

func TestMarkerPaletteKeepsValueIsolation(t *testing.T) {
	p := NoColorPalette()
	original := NewMarker(p, "en")
	before := original.Draft("small", "big", 1, "")
	p.Marker = p.Marker.Transform(func(s string) string { return "changed-parent " + s })
	if original.Draft("small", "big", 1, "") != before {
		t.Fatal("source palette mutation changed marker")
	}
	copy := original
	copy.p.Marker = copy.p.Marker.Transform(func(s string) string { return "changed-copy " + s })
	if original.Draft("small", "big", 1, "") != before {
		t.Fatal("copied marker mutation changed original")
	}
	if copy.Draft("small", "big", 1, "") == before {
		t.Fatal("copy mutation was ignored")
	}
}
func TestMarkerZeroValue(t *testing.T) {
	var marker Marker
	if marker.Done(1, 2) == "" {
		t.Fatal("zero-value marker did not render")
	}
}

var markerBenchmarkSink string

func BenchmarkMarkerToolResultFull(b *testing.B) {
	p := NoColorPalette()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		marker := NewMarker(p, "en")
		markerBenchmarkSink = marker.ToolResultFull("read_file", "package main\nfunc main() {}", false)
	}
}

func TestToolDisplayOutputKeepsPlainAndUnsupportedJSON(t *testing.T) {
	for _, input := range []string{"", " \n\t", "plain\noutput\n", "[1, 2]", "null", "123", "\"text\"", "{}", "{invalid", `{"stdout":null,"stderr":"warning"}`, "\u00a0plain\n中文"} {
		if got := toolDisplayOutput(input); got != input {
			t.Errorf("changed raw output %q to %q", input, got)
		}
	}
}

func TestToolDisplayOutputUnwrapsProcessJSON(t *testing.T) {
	cases := []struct{ input, want string }{
		{`{"stdout":"one\r\n","stderr":""}`, "one"},
		{" \t\n" + `{"stdout":"中文 😀\n","stderr":"warning\r\n"}` + "\n ", "中文 😀\nstderr:\nwarning"},
		{`{"stdout":"","stderr":""}`, ""},
		{`{"stdout":"","stderr":"warning"}`, "stderr:\nwarning"},
		{`{"stdout":false,"stderr":"warning"}`, `{"stdout":false,"stderr":"warning"}`},
	}
	for _, tc := range cases {
		if got := toolDisplayOutput(tc.input); got != tc.want {
			t.Errorf("toolDisplayOutput(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestMarkerToolResultVisibleLineBoundaries(t *testing.T) {
	marker := NewMarker(NoColorPalette(), "en")
	for _, expanded := range []bool{false, true} {
		limit := 4
		if expanded {
			limit = 40
		}
		for _, count := range []int{1, limit - 1, limit, limit + 1, 40000} {
			lines := make([]string, count)
			for i := range lines {
				lines[i] = fmt.Sprintf("row-%05d 中文 😀", i+1)
			}
			input := strings.Join(lines, "\n") + "\n\n"
			got := marker.ToolResultFull("read_file", input, expanded)
			visible := count
			if visible > limit {
				visible = limit
			}
			if n := strings.Count(got, "    │ "); n != visible {
				t.Fatalf("expanded=%v count=%d: rendered %d lines, want %d", expanded, count, n, visible)
			}
			for _, line := range lines[:visible] {
				if !strings.Contains(got, "    │ "+line) {
					t.Fatalf("missing visible line %q", line)
				}
			}
			if count > limit {
				if strings.Contains(got, lines[limit]) {
					t.Fatalf("hidden line was rendered: %q", lines[limit])
				}
				if !strings.Contains(got, fmt.Sprintf("%d more", count-limit)) {
					t.Fatalf("incorrect remainder count: %q", got)
				}
			} else if strings.Contains(got, "more ·") {
				t.Fatalf("untruncated output has remainder: %q", got)
			}
			label := "lines"
			if count == 1 {
				label = "line"
			}
			if !strings.Contains(got, fmt.Sprintf("%d %s ·", count, label)) {
				t.Fatalf("incorrect total line count: %q", got)
			}
		}
	}
}

func BenchmarkMarkerToolResultLargePayload(b *testing.B) {
	row := "src/example.go:123: warning: Zażółć 中文 😀 inspect this line.\n"
	raw := strings.Repeat(row, (1<<20)/len(row))
	structured, err := json.Marshal(map[string]string{"stdout": raw, "stderr": "warning"})
	if err != nil {
		b.Fatal(err)
	}
	marker := NewMarker(NoColorPalette(), "en")
	for _, sample := range []struct{ name, output string }{{"raw-lines", raw}, {"json-lines", string(structured)}} {
		for _, expanded := range []bool{false, true} {
			b.Run(fmt.Sprintf("%s/expanded=%v", sample.name, expanded), func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					markerBenchmarkSink = marker.ToolResultFull("read_file", sample.output, expanded)
				}
			})
		}
	}
}
