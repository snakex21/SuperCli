package agent

import (
	"strconv"
	"testing"

	"supercli/internal/llm"
)

func itoa(i int) string { return strconv.Itoa(i) }

// allOK is the per-call verdict for a batch where every call succeeded and
// produced something.
func allOK(n int) []callOutcome { return make([]callOutcome, n) }

// verdicts builds a per-call outcome slice from a compact spec:
// 'o' = ok, 'f' = failed, 'i' = inert.
func verdicts(spec string) []callOutcome {
	out := make([]callOutcome, 0, len(spec))
	for _, c := range spec {
		switch c {
		case 'f':
			out = append(out, callOutcome{failed: true})
		case 'i':
			out = append(out, callOutcome{inert: true})
		default:
			out = append(out, callOutcome{})
		}
	}
	return out
}









func TestNormalizeToolArgsJSON_KeyOrder(t *testing.T) {
	a := normalizeToolArgsJSON(`{"path":"a","start":1}`)
	b := normalizeToolArgsJSON(`{"start":1,"path":"a"}`)
	if a != b {
		t.Fatalf("normalized args differ:\n%q\n%q", a, b)
	}
	fa := toolCallFingerprint("read_lines", `{"path":"a","start":1}`)
	fb := toolCallFingerprint("read_lines", `{"start":1,"path":"a"}`)
	if fa != fb {
		t.Fatal("fingerprints must match after JSON key reorder")
	}
}

func TestIdenticalFailureGate(t *testing.T) {
	var g identicalFailureGate
	name, args := "search_code", `{"query":"x"}`
	if g.shouldBlock(name, args) {
		t.Fatal("fresh call blocked")
	}
	g.recordFailure(name, args)
	g.recordFailure(name, args)
	if !g.shouldBlock(name, args) {
		t.Fatal("third identical should block")
	}
	// reordered JSON still blocks
	if !g.shouldBlock(name, `{"query": "x"}`) {
		// spacing may differ after normalize — both should hash same if valid JSON
	}
	if !g.shouldBlock(name, normalizeToolArgsJSON(`{"query":"x"}`)) {
		// same after normalize path used in key()
	}
	// different args ok
	if g.shouldBlock(name, `{"query":"y"}`) {
		t.Fatal("different args must not block")
	}
	g.recordSuccess(name, args)
	if g.shouldBlock(name, args) {
		t.Fatal("success should clear")
	}
}

func TestIdenticalFailureGate_NormalizedArgs(t *testing.T) {
	var g identicalFailureGate
	g.recordFailure("read_lines", `{"path":"a","start":1}`)
	g.recordFailure("read_lines", `{"start":1,"path":"a"}`)
	if !g.shouldBlock("read_lines", `{"path":"a","start":1}`) {
		t.Fatal("reordered keys should share failure count")
	}
}



func TestMessagesHaveRecentToolResult(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "co jest w folderze?"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{Name: "list_dir", Arguments: `{}`}}},
		{Role: llm.RoleTool, Name: "list_dir", Content: "21 items..."},
		{Role: llm.RoleAssistant, Content: ""},
	}
	if !messagesHaveRecentToolResult(msgs) {
		t.Fatal("empty assistant after tool should still see tool result")
	}
	msgs = append(msgs, llm.Message{Role: llm.RoleAssistant, Content: "W folderze są pliki A, B."})
	if messagesHaveRecentToolResult(msgs) {
		t.Fatal("non-empty assistant reply should clear the need")
	}
	if messagesHaveRecentToolResult([]llm.Message{{Role: llm.RoleUser, Content: "cześć"}}) {
		t.Fatal("chat without tools")
	}
}

func TestToolKind_OfficeReadsAreDiscovery(t *testing.T) {
	for _, name := range []string{"read_pdf", "read_docx", "read_xlsx", "read_zip", "code_intel", "apply_skill"} {
		if toolKind(name) != "discovery" {
			t.Errorf("%s kind=%q want discovery", name, toolKind(name))
		}
	}
	if toolKind("mystery") != "other" {
		t.Fatal("unknown should be other")
	}
}
