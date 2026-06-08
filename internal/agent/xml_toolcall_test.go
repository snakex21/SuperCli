package agent

import (
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestExtractXMLToolCalls_Basic(t *testing.T) {
	text := `Let me read the file.
<tool_call>
<function=read_lines>
<parameter=file>test.txt</parameter>
<parameter=from>1</parameter>
<parameter=to>10</parameter>
</function>
</tool_call>
Done.`

	tcs, before := extractXMLToolCalls(text)
	if len(tcs) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(tcs))
	}
	if tcs[0].Name != "read_lines" {
		t.Errorf("Name = %q, want read_lines", tcs[0].Name)
	}
	if !strings.Contains(tcs[0].Arguments, "test.txt") {
		t.Errorf("Arguments missing file: %s", tcs[0].Arguments)
	}
	if !strings.Contains(tcs[0].Arguments, "from") {
		t.Errorf("Arguments missing from: %s", tcs[0].Arguments)
	}
	if before != "Let me read the file.\n" {
		t.Errorf("before = %q", before)
	}
}

func TestExtractXMLToolCalls_Simple(t *testing.T) {
	text := `<tool_call><function=search><parameter=query>hello world</parameter></function></tool_call>`
	tcs, _ := extractXMLToolCalls(text)
	if len(tcs) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(tcs))
	}
	if tcs[0].Name != "search" {
		t.Errorf("Name = %q", tcs[0].Name)
	}
	if !strings.Contains(tcs[0].Arguments, "hello world") {
		t.Errorf("Arguments = %s", tcs[0].Arguments)
	}
}

func TestExtractXMLToolCalls_NoTags(t *testing.T) {
	text := "plain text without any XML"
	tcs, before := extractXMLToolCalls(text)
	if len(tcs) != 0 {
		t.Errorf("expected 0 tool calls, got %d", len(tcs))
	}
	if before != "" {
		t.Errorf("before should be empty, got %q", before)
	}
}

func TestExtractXMLToolCalls_Incomplete(t *testing.T) {
	// Streaming: <tool_call> opened but not closed.
	text := "<tool_call><function=read_lines><parameter=file>test.txt"
	tcs, _ := extractXMLToolCalls(text)
	if len(tcs) != 0 {
		t.Errorf("incomplete XML should not yield tool calls, got %d", len(tcs))
	}
}

func TestExtractXMLToolCalls_MultipleParams(t *testing.T) {
	text := `<tool_call><function=edit_line><parameter=file>test.txt</parameter><parameter=line>5</parameter><parameter=new_content>hello</parameter></function></tool_call>`
	tcs, _ := extractXMLToolCalls(text)
	if len(tcs) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(tcs))
	}
	if tcs[0].Name != "edit_line" {
		t.Errorf("Name = %q", tcs[0].Name)
	}
	// Should have 3 parameters.
	if strings.Count(tcs[0].Arguments, `"`) < 6 {
		t.Errorf("expected 3 key-value pairs in args: %s", tcs[0].Arguments)
	}
}

func TestExtractXMLToolCalls_BashTool(t *testing.T) {
	text := `<tool_call>
<function=ctx_execute>
<parameter=command>["cmd","/c","dir"]</parameter>
</function>
</tool_call>`
	tcs, _ := extractXMLToolCalls(text)
	if len(tcs) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(tcs))
	}
	if tcs[0].Name != "ctx_execute" {
		t.Errorf("Name = %q", tcs[0].Name)
	}
	if !strings.Contains(tcs[0].Arguments, `["cmd","/c","dir"]`) {
		t.Errorf("command array not preserved: %s", tcs[0].Arguments)
	}
}

func TestExtractXMLToolCalls_EmptyBlock(t *testing.T) {
	text := "<tool_call></tool_call>"
	tcs, _ := extractXMLToolCalls(text)
	if len(tcs) != 0 {
		t.Errorf("empty block should yield 0 tool calls, got %d", len(tcs))
	}
}

func TestExtractXMLFuncName(t *testing.T) {
	tests := []struct {
		block string
		want  string
	}{
		{"<function=read_lines>...</function>", "read_lines"},
		{"<function=bash/>", "bash"},
		{"<function=search ></function>", "search"},
		{"no function here", ""},
	}
	for _, tt := range tests {
		got := extractXMLFuncName(tt.block)
		if got != tt.want {
			t.Errorf("extractXMLFuncName(%q) = %q, want %q", tt.block, got, tt.want)
		}
	}
}

func TestJsonString(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"hello", `"hello"`},
		{"", `""`},
		{`{"key":"value"}`, `{"key":"value"}`}, // already JSON object
		{`[1,2,3]`, `[1,2,3]`},                 // already JSON array
		{`say "hello"`, `"say \"hello\""`},
	}
	for _, tt := range tests {
		got := jsonString(tt.in)
		if got != tt.want {
			t.Errorf("jsonString(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestXMLToolCall_SmokeConvertsToToolCall(t *testing.T) {
	// Verify the helper produces a valid llm.ToolCall.
	tcs, _ := extractXMLToolCalls(
		`<tool_call><function=read_lines><parameter=file>f.txt</parameter></function></tool_call>`)
	if len(tcs) != 1 {
		t.Fatal("no tool call")
	}
	_ = tcs[0] // ensure it's a valid llm.ToolCall
	var tc llm.ToolCall = tcs[0]
	if tc.Name != "read_lines" {
		t.Errorf("wrong name")
	}
	if tc.Arguments == "" {
		t.Error("arguments empty")
	}
}
