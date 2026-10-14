package app

import (
	"testing"

	"supercli/internal/llm"
	"supercli/internal/system/stats"
)

func TestBatchContextProbeFindsPrefixBreaks(t *testing.T) {
	p := newBatchContextProbe(true)
	sys := llm.Message{Role: llm.RoleSystem, Content: "you are a coding agent"}
	user := llm.Message{Role: llm.RoleUser, Content: "fix the bug"}
	asst := llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "1", Name: "read_file", Arguments: `{"path":"a.go"}`}}}
	tool := llm.Message{Role: llm.RoleTool, ToolCallID: "1", Content: "package a"}
	tools := []llm.ToolDef{{Name: "small", Description: "x"}, {Name: "big", Description: "a much longer description of a tool", Schema: `{"type":"object","properties":{"path":{"type":"string"}}}`}}

	stamp := func(s string) llm.Message { return llm.Message{Role: llm.RoleSystem, Content: "now " + s} }
	p.observe(llm.PurposeMain, []llm.Message{sys, user, stamp("12:00")}, tools)
	p.observe(llm.PurposeAdvisor, []llm.Message{user}, nil) // helpers are not the main prefix
	// Replacing only the trailing stamp keeps the prefix.
	p.observe(llm.PurposeMain, []llm.Message{sys, user, asst, tool, stamp("12:01")}, tools)
	changed := sys
	changed.Content += " (time 12:01)"
	p.observe(llm.PurposeMain, []llm.Message{changed, user, asst, tool, stamp("12:02")}, tools[:1])

	got := p.report([]stats.Call{
		{Purpose: llm.PurposeMain, TokensIn: 100, TokensCached: 0, TTFTUs: 2_000},
		{Purpose: llm.PurposeAdvisor, TokensIn: 7},
		{Purpose: llm.PurposeMain, TokensIn: 150, TokensCached: 90},
		{Purpose: llm.PurposeMain, TokensIn: 150},
	})
	if len(got.Calls) != 3 {
		t.Fatalf("calls=%+v", got.Calls)
	}
	first, second, third := got.Calls[0], got.Calls[1], got.Calls[2]
	if first.System == 0 || first.Tail == 0 || first.ToolDefs == 0 || first.PrefixBreak || first.Input != 100 || first.TTFTMs != 2 {
		t.Fatalf("first=%+v", first)
	}
	if second.SharedPrefix != 2 || second.PrefixBreak || second.ToolsChanged || second.Input != 150 || second.Cached != 90 || second.History <= first.History {
		t.Fatalf("second=%+v", second)
	}
	if third.SharedPrefix != 0 || !third.PrefixBreak || !third.ToolsChanged {
		t.Fatalf("third=%+v", third)
	}
	if got.PrefixBreaks != 1 || got.ToolsChanged != 1 {
		t.Fatalf("totals=%+v", got)
	}
	if len(got.Tools) != 2 || got.Tools[0].Name != "big" || got.Tools[0].Tokens < got.Tools[1].Tokens {
		t.Fatalf("tool sizes=%+v", got.Tools)
	}
	if newBatchContextProbe(false).report(nil) != nil {
		t.Fatal("disabled probe must report nothing")
	}
}
