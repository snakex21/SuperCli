package llm

import (
	"fmt"
	"strings"
	"testing"
)

var benchmarkRequestBreakdown RequestBreakdown

func BenchmarkRequestBreakdownHistory(b *testing.B) {
	for _, size := range []struct {
		name                     string
		messages, tools, repeats int
	}{
		{"small", 12, 8, 8}, {"large", 240, 80, 256},
	} {
		b.Run(size.name, func(b *testing.B) {
			msgs := make([]Message, size.messages)
			for i := range msgs {
				msgs[i] = Message{Role: RoleUser, Content: strings.Repeat("Inspect this source file and explain its behavior.\n", size.repeats)}
				if i%3 == 1 {
					msgs[i].Role = RoleAssistant
					msgs[i].ToolCalls = []ToolCall{{Name: "write_file", Arguments: `{"path":"src/main.go","content":"` + strings.Repeat("func example() { return }\\n", size.repeats*4) + `"}`}}
				}
				if i%3 == 2 {
					msgs[i].Role = RoleTool
				}
			}
			defs := make([]ToolDef, size.tools)
			for i := range defs {
				defs[i] = ToolDef{Name: fmt.Sprintf("tool_%d", i), Description: strings.Repeat("Inspect project content. ", 10), Schema: `{"type":"object","properties":{"path":{"type":"string"}}}`}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchmarkRequestBreakdown = EstimateRequestBreakdown(msgs, defs)
			}
		})
	}
}

// Pin the prior two-pass accounting, including independent integer rounding.
func TestRequestBreakdownSinglePassMatchesReference(t *testing.T) {
	roles := []Role{RoleSystem, RoleUser, RoleAssistant, RoleTool, Role("other")}
	for _, role := range roles {
		for n := 0; n < 100; n++ {
			msg := Message{Role: role, Content: strings.Repeat("ą x\t\n", n), ToolCalls: []ToolCall{{Name: "edit", Arguments: strings.Repeat("z\r\n", n+1)}, {Name: "read", Arguments: "{}"}}}
			if n%2 == 0 {
				msg.Parts = []ContentPart{{Type: PartTypeText, Text: "part text ą\n"}, {Type: PartTypeReasoning}, {Type: PartTypeReasoning, Reasoning: &ReasoningBlock{Tokens: 7}}}
			}
			if n%5 == 0 {
				msg.ToolCalls = nil
			}
			tokens := referenceMessageEstimate(msg)
			if got := EstimateMessageTokens(msg); got != tokens {
				t.Fatalf("message role=%s n=%d: got %d want %d", role, n, got, tokens)
			}
			var want RequestBreakdown
			switch role {
			case RoleSystem:
				want.System = tokens
			case RoleUser:
				want.User = tokens
			case RoleTool:
				want.Tool = tokens
			case RoleAssistant:
				toolTokens := referenceMessageEstimate(Message{Role: RoleAssistant, ToolCalls: msg.ToolCalls}) - 16
				want.Tool = toolTokens
				want.Assistant = tokens - toolTokens
			default:
				want.Other = tokens
			}
			if got := EstimateRequestBreakdown([]Message{msg}, nil); got != want {
				t.Fatalf("role=%s n=%d: got %+v want %+v", role, n, got, want)
			}
		}
	}
}

// Original formula, retained independently of the single-pass implementation.
func referenceMessageEstimate(m Message) int {
	b := nonWhitespaceLen(m.Content)
	reasoning := 0
	for _, p := range m.Parts {
		if p.Type == PartTypeText {
			b += nonWhitespaceLen(p.Text)
		} else if p.Type == PartTypeReasoning {
			reasoning += p.Reasoning.EstimateTokens()
		}
	}
	for _, tc := range m.ToolCalls {
		b += nonWhitespaceLen(tc.Name) + nonWhitespaceLen(tc.Arguments)
	}
	return b/3 + 16 + reasoning
}

var benchmarkMessageEstimate int

func BenchmarkPlainMessageEstimate(b *testing.B) {
	m := Message{Role: RoleUser, Content: strings.Repeat("Explain this code.\n", 256)}
	b.Run("reference", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			benchmarkMessageEstimate = referenceMessageEstimate(m)
		}
	})
	b.Run("single_pass", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			benchmarkMessageEstimate = EstimateMessageTokens(m)
		}
	})
}

func TestRequestBreakdownWithoutCallsPreservesOverflowAttribution(t *testing.T) {
	msg := Message{Role: RoleAssistant, Parts: []ContentPart{{Type: PartTypeReasoning, Reasoning: &ReasoningBlock{Tokens: int(^uint(0) >> 1)}}}}
	want := RequestBreakdown{Assistant: referenceMessageEstimate(msg)}
	if got := EstimateRequestBreakdown([]Message{msg}, nil); got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
}
