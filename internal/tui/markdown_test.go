package tui

import (
	"strings"
	"testing"
)

// --- Markdown rendering ---

func TestRenderMarkdown_Headings(t *testing.T) {
	p := DefaultPalette()
	tests := []struct {
		input    string
		contains string
	}{
		{"## Hello", "Hello"},
		{"### Subhead", "Subhead"},
		{"plain text", "plain text"},
		{"## ", ""}, // empty heading
	}
	for _, tt := range tests {
		out := renderAssistantMarkdown(tt.input, p, false)
		if tt.contains != "" && !strings.Contains(out, tt.contains) {
			t.Errorf("input=%q: expected to contain %q, got %q", tt.input, tt.contains, out)
		}
	}
}

func TestRenderMarkdown_Bold(t *testing.T) {
	p := DefaultPalette()
	out := renderAssistantMarkdown("Hello **world** today", p, false)
	if !strings.Contains(out, "world") {
		t.Errorf("bold text missing: %q", out)
	}
	// Should NOT contain literal **
	if strings.Contains(out, "**") {
		t.Errorf("literal ** not stripped: %q", out)
	}
}

func TestRenderMarkdown_Code(t *testing.T) {
	p := DefaultPalette()
	out := renderAssistantMarkdown("call `tool_search` first", p, false)
	if !strings.Contains(out, "tool_search") {
		t.Errorf("code text missing: %q", out)
	}
	if strings.Contains(out, "`") {
		t.Errorf("literal backtick not stripped: %q", out)
	}
}

func TestRenderMarkdown_ListItems(t *testing.T) {
	p := DefaultPalette()
	out := renderAssistantMarkdown("- item one\n- item two", p, false)
	if !strings.Contains(out, "\u2022 item one") {
		t.Errorf("bullet not applied: %q", out)
	}
	if strings.Contains(out, "- ") && !strings.Contains(out, "\u2022") {
		t.Errorf("dash not converted to bullet")
	}
}

func TestRenderMarkdown_MultipleLines(t *testing.T) {
	p := DefaultPalette()
	text := "## Title\n\nSome **bold** text\n- list\n- items\n\n`code here`"
	out := renderAssistantMarkdown(text, p, false)
	// Inline markdown chars (** and `) should be stripped.
	// ## prefix is kept as part of heading display.
	// - list dashes become bullets.
	for _, raw := range []string{"**", "`"} {
		if strings.Contains(out, raw) {
			t.Errorf("raw markdown %q not stripped from output", raw)
		}
	}
}

func TestRenderMarkdown_StripBoldLiteral(t *testing.T) {
	p := DefaultPalette()
	out := renderAssistantMarkdown("**hello**", p, false)
	if strings.Contains(out, "**") {
		t.Errorf("literal ** not stripped: %q", out)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("bold content missing: %q", out)
	}
}

func TestRenderMarkdown_EmptyInput(t *testing.T) {
	p := DefaultPalette()
	out := renderAssistantMarkdown("", p, false)
	if out != "" {
		t.Errorf("empty input should produce empty output, got %q", out)
	}
}

func TestRenderMarkdown_CollapsedDoesNotAffectMarkdown(t *testing.T) {
	p := DefaultPalette()
	out := renderAssistantMarkdown("**bold** and `code`", p, true)
	if strings.Contains(out, "**") || strings.Contains(out, "`") {
		t.Errorf("collapsed should still render inline markdown")
	}
}

// --- Thinking blocks ---

func TestRenderThinking_NoTags(t *testing.T) {
	p := DefaultPalette()
	out := renderAssistantMarkdown("Hello world", p, false)
	if strings.Contains(out, "Thinking") {
		t.Errorf("no thinking tags, should not show Thinking header: %q", out)
	}
}

func TestRenderThinking_ClosedBlock(t *testing.T) {
	p := DefaultPalette()
	text := "Before\n<thinking>\nI need to analyze\n</thinking>\nAfter"
	out := renderAssistantMarkdown(text, p, false)
	if !strings.Contains(out, "Thinking:") {
		t.Errorf("closed thinking block should show header: %q", out)
	}
	if !strings.Contains(out, "I need to analyze") {
		t.Errorf("thinking content missing: %q", out)
	}
	if !strings.Contains(out, "Before") {
		t.Errorf("text before thinking block missing: %q", out)
	}
	if !strings.Contains(out, "After") {
		t.Errorf("text after thinking block missing: %q", out)
	}
}

func TestRenderThinking_ReasoningTagKeepsFinalAnswerSeparate(t *testing.T) {
	p := DefaultPalette()
	out := renderAssistantMarkdown(`<thinking>The user said "hi" in Polish.</thinking>
Cześć! W czym mogę pomóc?`, p, false)
	if !strings.Contains(out, "Thinking:") {
		t.Fatalf("missing thinking header: %q", out)
	}
	if !strings.Contains(out, `The user said "hi" in Polish.`) {
		t.Fatalf("missing reasoning: %q", out)
	}
	if !strings.Contains(out, "Cześć! W czym mogę pomóc?") {
		t.Fatalf("final answer missing: %q", out)
	}
}

func TestRenderThinking_UnclosedBlock(t *testing.T) {
	p := DefaultPalette()
	// Simulates streaming: <thinking> without </thinking>
	text := "Response\n<thinking>\nanalyzing..."
	out := renderAssistantMarkdown(text, p, false)
	if !strings.Contains(out, "Thinking:") {
		t.Errorf("unclosed thinking should show header: %q", out)
	}
	if !strings.Contains(out, "analyzing...") {
		t.Errorf("streaming thinking content missing: %q", out)
	}
}

func TestRenderThinking_Collapsed(t *testing.T) {
	p := DefaultPalette()
	text := "<thinking>\nsecret plan\n</thinking>\nvisible"
	out := renderAssistantMarkdown(text, p, true)
	if strings.Contains(out, "secret plan") {
		t.Errorf("collapsed should hide thinking content: %q", out)
	}
	if !strings.Contains(out, "hidden") {
		t.Errorf("collapsed should show hidden indicator: %q", out)
	}
	if !strings.Contains(out, "visible") {
		t.Errorf("non-thinking text should still be visible: %q", out)
	}
}

func TestRenderThinking_MultipleBlocks(t *testing.T) {
	p := DefaultPalette()
	text := "<thinking>\nplan A\n</thinking>\nmid\n<thinking>\nplan B\n</thinking>\nend"
	out := renderAssistantMarkdown(text, p, false)
	if strings.Count(out, "Thinking:") != 2 {
		t.Errorf("expected 2 Thinking headers, got: %q", out)
	}
	if !strings.Contains(out, "plan A") || !strings.Contains(out, "plan B") {
		t.Errorf("both thinking blocks should be visible")
	}
}

func TestRenderThinking_EmptyBlock(t *testing.T) {
	p := DefaultPalette()
	out := renderAssistantMarkdown("<thinking></thinking>after", p, false)
	if strings.Contains(out, "Thinking:") {
		t.Errorf("empty thinking block should not show header")
	}
	if !strings.Contains(out, "after") {
		t.Errorf("text after empty block missing: %q", out)
	}
}

func TestRenderThinking_WhitespaceOnly(t *testing.T) {
	p := DefaultPalette()
	out := renderAssistantMarkdown("<thinking>  \n  </thinking>", p, false)
	if strings.Contains(out, "Thinking:") {
		t.Errorf("whitespace-only thinking block should not render: %q", out)
	}
}

func TestRenderThinking_BoldInsideThinking(t *testing.T) {
	p := DefaultPalette()
	text := "<thinking>\n**important** note\n</thinking>"
	out := renderAssistantMarkdown(text, p, false)
	// Bold inside thinking should still render (inline markdown still applies)
	if !strings.Contains(out, "important") {
		t.Errorf("bold inside thinking block should render")
	}
}

// --- splitThinking edge cases ---

func TestSplitThinking_NoTags(t *testing.T) {
	segs := splitThinking("plain text")
	if len(segs) != 1 || segs[0].thinking {
		t.Errorf("plain text should be 1 non-thinking segment")
	}
}

func TestSplitThinking_OnlyOpenTag(t *testing.T) {
	segs := splitThinking("<thinking>")
	if len(segs) != 1 || !segs[0].thinking {
		t.Errorf("only <thinking> should be 1 thinking segment (streaming)")
	}
}

func TestSplitThinking_TagInMiddle(t *testing.T) {
	segs := splitThinking("hi<thinking>plan</thinking>bye")
	if len(segs) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(segs))
	}
	if segs[0].thinking || segs[0].text != "hi" {
		t.Errorf("seg[0] should be non-thinking 'hi': %+v", segs[0])
	}
	if !segs[1].thinking || segs[1].text != "plan" {
		t.Errorf("seg[1] should be thinking 'plan': %+v", segs[1])
	}
	if segs[2].thinking || segs[2].text != "bye" {
		t.Errorf("seg[2] should be non-thinking 'bye': %+v", segs[2])
	}
}

// --- Chat toggle ---

func TestChat_ToggleThinking(t *testing.T) {
	c := newChat(80)
	if c.thinkingCollapsed {
		t.Fatal("default should be not collapsed")
	}
	c.toggleThinking()
	if !c.thinkingCollapsed {
		t.Fatal("after toggle, should be collapsed")
	}
	c.toggleThinking()
	if c.thinkingCollapsed {
		t.Fatal("after second toggle, should be not collapsed")
	}
}

// --- Heuristic thinking detection ---

func TestHeuristicThinking_TheUser(t *testing.T) {
	p := DefaultPalette()
	text := "The user is asking about climate change.\nI should provide a concise answer.\n\n## Climate Change\nIt is caused by greenhouse gases."
	out := renderAssistantMarkdown(text, p, false)
	if !strings.Contains(out, "Thinking:") {
		t.Fatalf("should detect heuristic thinking, got: %q", out)
	}
	if !strings.Contains(out, "Climate Change") {
		t.Errorf("heading should still be visible: %q", out)
	}
}

func TestHeuristicThinking_Actually(t *testing.T) {
	p := DefaultPalette()
	text := "Actually, since this is a simple question,\nI can answer directly.\n\nHere is the answer: 42"
	out := renderAssistantMarkdown(text, p, false)
	if !strings.Contains(out, "Thinking:") {
		t.Errorf("'Actually' should trigger thinking: %q", out)
	}
	if !strings.Contains(out, "Here is the answer") {
		t.Errorf("response text missing: %q", out)
	}
}

func TestHeuristicThinking_HeadingBreaksThinking(t *testing.T) {
	p := DefaultPalette()
	text := "I'll look into this.\nLet me analyze.\n## Solution\nThe fix is simple."
	out := renderAssistantMarkdown(text, p, false)
	if !strings.Contains(out, "Thinking:") {
		t.Fatal("should detect thinking before heading")
	}
	if !strings.Contains(out, "## Solution") {
		t.Fatal("heading should break thinking")
	}
}

func TestHeuristicThinking_NoFalsePositive(t *testing.T) {
	p := DefaultPalette()
	text := "Here is the answer to your question.\nThe solution involves three steps.\n\n- step one\n- step two"
	out := renderAssistantMarkdown(text, p, false)
	// "The solution" doesn't start with "the user" or other prefixes.
	// Should NOT show Thinking.
	if strings.Contains(out, "Thinking:") {
		t.Errorf("should not trigger on normal text: %q", out)
	}
}

func TestHeuristicThinking_IAm(t *testing.T) {
	p := DefaultPalette()
	text := "I'm going to search for that file.\nFirst, I'll check the directory."
	out := renderAssistantMarkdown(text, p, false)
	if !strings.Contains(out, "Thinking:") {
		t.Errorf("'I'm' should trigger thinking: %q", out)
	}
}

func TestHeuristicThinking_CodeBlock(t *testing.T) {
	p := DefaultPalette()
	text := "Let me check the files.\n```bash\nls -la\n```"
	out := renderAssistantMarkdown(text, p, false)
	if !strings.Contains(out, "Thinking:") {
		t.Errorf("'Let me' should trigger thinking before code block: %q", out)
	}
}

func TestIsHeuristicThinkingLine(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"The user wants", true},
		{"I should check", true},
		{"I need to verify", true},
		{"Actually, this is", true},
		{"Let me think", true},
		{"I'll try", true},
		{"I might consider", true},
		{"I can do that", true},
		{"I think so", true},
		{"I will respond", true},
		{"I am ready", true},
		{"I'm going", true},
		{"  the user said", true}, // trimmed
		{"Hello world", false},
		{"The answer is 42", false},
		{"Here is the code:", false},
		{"", false},
	}
	for _, tt := range tests {
		got := isHeuristicThinkingLine(tt.line)
		if got != tt.want {
			t.Errorf("isHeuristicThinkingLine(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}

// --- Heading + inline markdown ---

func TestRenderMarkdown_HeadingWithBold(t *testing.T) {
	p := DefaultPalette()
	out := renderAssistantMarkdown("### **Nagłówek**", p, false)
	// ** should be stripped (bold applied via lipgloss).
	if strings.Contains(out, "**") {
		t.Errorf("raw ** not stripped: %q", out)
	}
	// Should contain the heading text.
	if !strings.Contains(out, "Nagłówek") {
		t.Errorf("heading text missing: %q", out)
	}
	// ### prefix is kept as part of heading display.
}

func TestRenderMarkdown_BoldText(t *testing.T) {
	p := DefaultPalette()
	out := renderAssistantMarkdown("**bold text**", p, false)
	if strings.Contains(out, "**") {
		t.Errorf("raw ** not stripped: %q", out)
	}
	if !strings.Contains(out, "bold text") {
		t.Errorf("bold text missing: %q", out)
	}
}

func TestRenderMarkdown_StarList(t *testing.T) {
	p := DefaultPalette()
	out := renderAssistantMarkdown("* item1\n* item2", p, false)
	if !strings.Contains(out, "\u2022 item1") {
		t.Errorf("bullet not applied: %q", out)
	}
	if strings.Contains(out, "* item") {
		t.Errorf("asterisk not converted to bullet: %q", out)
	}
}

func TestRenderMarkdown_MixedHeadingAndList(t *testing.T) {
	p := DefaultPalette()
	text := "### Tytuł\n* item1\n* item2"
	out := renderAssistantMarkdown(text, p, false)
	if !strings.Contains(out, "Tytuł") {
		t.Errorf("heading missing: %q", out)
	}
	if !strings.Contains(out, "\u2022 item1") {
		t.Errorf("list item 1 missing bullet: %q", out)
	}
	if !strings.Contains(out, "\u2022 item2") {
		t.Errorf("list item 2 missing bullet: %q", out)
	}
}

func TestRenderMarkdown_HeadingWithCode(t *testing.T) {
	p := DefaultPalette()
	out := renderAssistantMarkdown("## Użyj `tool_search`", p, false)
	if !strings.Contains(out, "tool_search") {
		t.Errorf("code in heading missing: %q", out)
	}
	if strings.Contains(out, "`") {
		t.Errorf("backticks not stripped: %q", out)
	}
}

// --- XML tool call filtering ---

func TestRenderMarkdown_XMLToolCallFiltered(t *testing.T) {
	p := DefaultPalette()
	text := "Before\n<tool_call><function=read_lines><parameter=file>test.txt</parameter></function></tool_call>\nAfter"
	out := renderAssistantMarkdown(text, p, false)
	if strings.Contains(out, "<tool_call>") {
		t.Errorf("XML tool call should be filtered: %q", out)
	}
	if strings.Contains(out, "</tool_call>") {
		t.Errorf("XML tool call close tag should be filtered: %q", out)
	}
	if !strings.Contains(out, "Before") {
		t.Errorf("text before XML block missing: %q", out)
	}
	if !strings.Contains(out, "After") {
		t.Errorf("text after XML block missing: %q", out)
	}
}

func TestRenderMarkdown_XMLToolCallMultilineFiltered(t *testing.T) {
	p := DefaultPalette()
	text := "text\n<tool_call>\n<function=bash>\n<parameter=command>ls</parameter>\n</function>\n</tool_call>\nmore text"
	out := renderAssistantMarkdown(text, p, false)
	if strings.Contains(out, "<tool_call>") {
		t.Errorf("multiline XML should be filtered: %q", out)
	}
	if !strings.Contains(out, "more text") {
		t.Errorf("text after XML missing: %q", out)
	}
}
