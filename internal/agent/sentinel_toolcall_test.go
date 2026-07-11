package agent

import (
	"context"
	"encoding/json"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// decodeArgs unmarshals a tool call's Arguments and fails on bad JSON.
func decodeArgs(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("Arguments not valid JSON: %v; raw=%s", err, raw)
	}
	return m
}

// --- happy path ---

func TestSentinel_NoArgs(t *testing.T) {
	tcs, before := extractSentinelToolCalls("«get_time»")
	if before != "" {
		t.Errorf("before = %q, want empty", before)
	}
	if len(tcs) != 1 || tcs[0].Name != "get_time" {
		t.Fatalf("tcs = %+v, want one get_time", tcs)
	}
	if m := decodeArgs(t, tcs[0].Arguments); len(m) != 0 {
		t.Errorf("args = %v, want empty object", m)
	}
}

func TestSentinel_OneArg(t *testing.T) {
	tcs, _ := extractSentinelToolCalls("«search_code\nquery: func main»")
	if len(tcs) != 1 {
		t.Fatalf("want 1 call, got %d", len(tcs))
	}
	if tcs[0].Name != "search_code" {
		t.Errorf("name = %q", tcs[0].Name)
	}
	m := decodeArgs(t, tcs[0].Arguments)
	if m["query"] != "func main" {
		t.Errorf("query = %v, want 'func main'", m["query"])
	}
}

func TestSentinel_MultipleArgs(t *testing.T) {
	in := "«edit_line\nfile: main.go\nline: 200\nexpected_old: return nil\nnew_content: return err»"
	tcs, _ := extractSentinelToolCalls(in)
	if len(tcs) != 1 {
		t.Fatalf("want 1 call, got %d", len(tcs))
	}
	m := decodeArgs(t, tcs[0].Arguments)
	if m["file"] != "main.go" {
		t.Errorf("file = %v", m["file"])
	}
	if m["line"] != "200" {
		t.Errorf("line = %v (note: string, mapping to int is the tool's job)", m["line"])
	}
	if m["expected_old"] != "return nil" {
		t.Errorf("expected_old = %v", m["expected_old"])
	}
	if m["new_content"] != "return err" {
		t.Errorf("new_content = %v", m["new_content"])
	}
}

// --- edge cases ---

func TestSentinel_TextBeforeBlock(t *testing.T) {
	tcs, before := extractSentinelToolCalls("Let me read that file.\n«read_lines\nfile: a.go»")
	if before != "Let me read that file.\n" {
		t.Errorf("before = %q", before)
	}
	if len(tcs) != 1 || tcs[0].Name != "read_lines" {
		t.Fatalf("tcs = %+v", tcs)
	}
}

func TestSentinel_NotYetClosed_Streaming(t *testing.T) {
	// Opening sentinel present, no closing yet: must buffer.
	tcs, before := extractSentinelToolCalls("«edit_line\nfile: ma")
	if tcs != nil || before != "" {
		t.Errorf("incomplete block should yield (nil,\"\"), got tcs=%+v before=%q", tcs, before)
	}
}

func TestSentinel_NoSentinelAtAll(t *testing.T) {
	tcs, before := extractSentinelToolCalls("just normal prose, no tools")
	if tcs != nil || before != "" {
		t.Errorf("no sentinel should yield (nil,\"\"), got tcs=%+v before=%q", tcs, before)
	}
}

func TestSentinel_BlankLinesIgnored(t *testing.T) {
	in := "«file_ops\n\naction: list\n\npath: .\n»"
	tcs, _ := extractSentinelToolCalls(in)
	if len(tcs) != 1 {
		t.Fatalf("want 1 call, got %d", len(tcs))
	}
	m := decodeArgs(t, tcs[0].Arguments)
	if m["action"] != "list" || m["path"] != "." {
		t.Errorf("args = %v", m)
	}
}

func TestSentinel_LineWithoutColonSkipped(t *testing.T) {
	in := "«grep\nthis line has no colon\npattern: TODO»"
	tcs, _ := extractSentinelToolCalls(in)
	if len(tcs) != 1 {
		t.Fatalf("want 1 call, got %d", len(tcs))
	}
	m := decodeArgs(t, tcs[0].Arguments)
	if len(m) != 1 || m["pattern"] != "TODO" {
		t.Errorf("args = %v, want only pattern:TODO", m)
	}
}

func TestSentinel_ValueWithColonAndPath(t *testing.T) {
	// Only the FIRST colon splits; the rest is value (URLs, ranges).
	in := "«web_fetch\nurl: https://example.com/x»"
	tcs, _ := extractSentinelToolCalls(in)
	m := decodeArgs(t, tcs[0].Arguments)
	if m["url"] != "https://example.com/x" {
		t.Errorf("url = %v, want full URL with colon", m["url"])
	}
}

func TestSentinel_QuotesEscaped(t *testing.T) {
	in := "«remember\ntext: she said \"hi\"»"
	tcs, _ := extractSentinelToolCalls(in)
	// The whole point: Arguments must still be valid JSON.
	m := decodeArgs(t, tcs[0].Arguments)
	if m["text"] != `she said "hi"` {
		t.Errorf("text = %v, want escaped quotes preserved", m["text"])
	}
}

func TestSentinel_NameWithLeadingBlankLine(t *testing.T) {
	in := "«\nedit_line\nfile: a.go»"
	tcs, _ := extractSentinelToolCalls(in)
	if len(tcs) != 1 || tcs[0].Name != "edit_line" {
		t.Fatalf("tcs = %+v, want edit_line", tcs)
	}
}

// --- error paths ---

func TestSentinel_EmptyBlockNoName(t *testing.T) {
	tcs, before := extractSentinelToolCalls("«»")
	if tcs != nil || before != "" {
		t.Errorf("empty block should be left as prose, got tcs=%+v before=%q", tcs, before)
	}
}

func TestSentinel_OnlyBlankInsideNoName(t *testing.T) {
	tcs, _ := extractSentinelToolCalls("«\n\n»")
	if tcs != nil {
		t.Errorf("nameless block should yield nil, got %+v", tcs)
	}
}

func TestSentinel_IDPrefixed(t *testing.T) {
	tcs, _ := extractSentinelToolCalls("«get_time»")
	if tcs[0].ID != "sentinel_get_time" {
		t.Errorf("ID = %q, want sentinel_get_time", tcs[0].ID)
	}
}

// --- integration: wired into consume() ---

// TestSentinel_ConsumeExtractsCall feeds a streamed message that
// ends in a sentinel block through consume() and asserts the call
// is extracted and the leading prose is emitted as a MessageEvent.
func TestSentinel_ConsumeExtractsCall(t *testing.T) {
	l := makeLoop(t, &stubProvider{name: "stub"}, tools.NewRegistry(), "base")

	ch := make(chan llm.Delta, 4)
	ch <- llm.Delta{Content: "I'll read it.\n"}
	ch <- llm.Delta{Content: "«read_lines\nfile: a.go\nfrom: 1\nto: 10»"}
	ch <- llm.Delta{FinishReason: "stop", Usage: &llm.Usage{Total: 3}}
	close(ch)

	out := make(chan Event, 16)
	_, toolCalls, _, err := l.consume(context.Background(), ch, out)
	close(out)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if len(toolCalls) != 1 || toolCalls[0].Name != "read_lines" {
		t.Fatalf("toolCalls = %+v, want one read_lines", toolCalls)
	}
	m := decodeArgs(t, toolCalls[0].Arguments)
	if m["file"] != "a.go" || m["from"] != "1" || m["to"] != "10" {
		t.Errorf("args = %v", m)
	}
	// The prose before the block must have been emitted.
	var sawProse bool
	for ev := range out {
		if me, ok := ev.(MessageEvent); ok && me.Text == "I'll read it.\n" {
			sawProse = true
		}
	}
	if !sawProse {
		t.Error("leading prose was not emitted as a MessageEvent")
	}
}

// TestSentinel_ConsumeSplitAcrossDeltas streams a sentinel block
// cut into many deltas — including the « and » markers split in
// half (mid-UTF-8, byte-wise) — and asserts the call is still
// extracted exactly as if it had arrived in one piece.
func TestSentinel_ConsumeSplitAcrossDeltas(t *testing.T) {
	l := makeLoop(t, &stubProvider{name: "stub"}, tools.NewRegistry(), "base")

	full := "prose first "
	// « = C2 AB, » = C2 BB: split each marker between its two bytes.
	parts := []string{
		"prose ", "first ",
		"\xc2", "\xab", "read_li", "nes\nfile: a", ".go\nfrom: 1\nto: 10", "\xc2", "\xbb",
		" trailing",
	}
	ch := make(chan llm.Delta, len(parts)+1)
	for _, p := range parts {
		ch <- llm.Delta{Content: p}
	}
	ch <- llm.Delta{FinishReason: "stop"}
	close(ch)

	out := make(chan Event, 64)
	text, toolCalls, _, err := l.consume(context.Background(), ch, out)
	close(out)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if len(toolCalls) != 1 || toolCalls[0].Name != "read_lines" {
		t.Fatalf("toolCalls = %+v, want one read_lines", toolCalls)
	}
	m := decodeArgs(t, toolCalls[0].Arguments)
	if m["file"] != "a.go" || m["from"] != "1" || m["to"] != "10" {
		t.Errorf("args = %v", m)
	}
	// Text after the block keeps accumulating on the remainder.
	if want := full + " trailing"; text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
	var sawProse bool
	for ev := range out {
		if me, ok := ev.(MessageEvent); ok && me.Text == full {
			sawProse = true
		}
	}
	if !sawProse {
		t.Error("prose before the split block was not emitted")
	}
}

// TestSentinel_ConsumeXMLSplitAcrossDeltas does the same for the
// XML <tool_call> fallback: markers cut mid-tag across deltas.
func TestSentinel_ConsumeXMLSplitAcrossDeltas(t *testing.T) {
	l := makeLoop(t, &stubProvider{name: "stub"}, tools.NewRegistry(), "base")

	parts := []string{
		"hi ", "<tool_c", "all>",
		"<function=get_", "time><param", "eter=tz>utc</parameter></func", "tion>",
		"</tool_", "call>",
	}
	ch := make(chan llm.Delta, len(parts)+1)
	for _, p := range parts {
		ch <- llm.Delta{Content: p}
	}
	ch <- llm.Delta{FinishReason: "stop"}
	close(ch)

	out := make(chan Event, 64)
	_, toolCalls, _, err := l.consume(context.Background(), ch, out)
	close(out)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if len(toolCalls) != 1 || toolCalls[0].Name != "get_time" {
		t.Fatalf("toolCalls = %+v, want one get_time", toolCalls)
	}
}

// TestSentinel_ConsumePlainTextUntouched ensures a normal streamed
// answer with no sentinel produces no tool calls.
func TestSentinel_ConsumePlainTextUntouched(t *testing.T) {
	l := makeLoop(t, &stubProvider{name: "stub"}, tools.NewRegistry(), "base")
	ch := make(chan llm.Delta, 2)
	ch <- llm.Delta{Content: "just a normal answer"}
	ch <- llm.Delta{FinishReason: "stop"}
	close(ch)
	out := make(chan Event, 8)
	text, toolCalls, _, err := l.consume(context.Background(), ch, out)
	close(out)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if len(toolCalls) != 0 {
		t.Errorf("want no tool calls, got %+v", toolCalls)
	}
	if text != "just a normal answer" {
		t.Errorf("text = %q", text)
	}
}
