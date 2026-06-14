package agent

import (
	"encoding/json"
	"testing"
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
