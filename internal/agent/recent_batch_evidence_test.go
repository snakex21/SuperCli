package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func mixedEvidenceHistory() []llm.Message {
	return []llm.Message{
		{Role: llm.RoleUser, Content: "Inspect source and a long build log."},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "large", Name: "read_lines", Arguments: `{"file":"build.log"}`},
			{ID: "small", Name: "read_lines", Arguments: `{"file":"retry.go"}`},
		}},
		{Role: llm.RoleTool, Name: "read_lines", ToolCallID: "large", Content: strings.Repeat("build detail\n", 500)},
		{Role: llm.RoleTool, Name: "read_lines", ToolCallID: "small", Content: "retry.go: RetryDelay=235, MaxAttempts=7"},
		{Role: llm.RoleAssistant, Content: "Inspected both files."},
		{Role: llm.RoleUser, Content: "What did the earlier read show?"},
	}
}

func assertEvidencePairs(t *testing.T, messages []llm.Message) {
	t.Helper()
	pending := map[string]bool{}
	for _, m := range messages {
		if m.Role == llm.RoleAssistant {
			for id, seen := range pending {
				if !seen {
					t.Fatalf("call without result: %s", id)
				}
			}
			pending = map[string]bool{}
			for _, call := range m.ToolCalls {
				if _, exists := pending[call.ID]; exists || call.ID == "" {
					t.Fatalf("invalid call ID %q", call.ID)
				}
				pending[call.ID] = false
			}
		}
		if m.Role == llm.RoleTool {
			seen, exists := pending[m.ToolCallID]
			if !exists || seen {
				t.Fatalf("orphan/duplicate result %q", m.ToolCallID)
			}
			pending[m.ToolCallID] = true
		}
	}
	for id, seen := range pending {
		if !seen {
			t.Fatalf("call without result: %s", id)
		}
	}
}

func TestRecentMixedBatchRetainsSmallCompleteExchange(t *testing.T) {
	input := mixedEvidenceHistory()
	before, _ := json.Marshal(input)
	got := omitResolvedToolHistory(input)
	assertEvidencePairs(t, got)
	if len(got) != 5 || len(got[1].ToolCalls) != 1 || got[1].ToolCalls[0].ID != "small" || got[2].ToolCallID != "small" || got[2].Content != input[3].Content {
		t.Fatalf("small finding lost: roles=%v", got)
	}
	if toolEvidenceBytes(got[1])+toolEvidenceBytes(got[2]) > recentToolEvidenceBytes {
		t.Fatal("budget exceeded")
	}
	after, _ := json.Marshal(input)
	if string(before) != string(after) {
		t.Fatal("canonical history mutated")
	}
	// Projection must be stable when prepared more than once.
	if again := omitResolvedToolHistory(got); !reflect.DeepEqual(again, got) {
		t.Fatal("projection is not stable")
	}
}

func TestRecentMixedBatchBudgetChronologyAndArguments(t *testing.T) {
	for seed := 0; seed < 40; seed++ {
		var history = []llm.Message{{Role: llm.RoleUser, Content: "inspect"}}
		for batch := 0; batch < 2; batch++ {
			m := llm.Message{Role: llm.RoleAssistant, Content: "Checking."}
			var results []llm.Message
			for n := 0; n < 8; n++ {
				id := fmt.Sprintf("%d-%d", batch, n)
				args := "{}"
				if n == 1 {
					args = strings.Repeat("argument", 1000)
				}
				m.ToolCalls = append(m.ToolCalls, llm.ToolCall{ID: id, Name: "read_lines", Arguments: args})
				results = append(results, llm.Message{Role: llm.RoleTool, Name: "read_lines", ToolCallID: id, Content: strings.Repeat("x", ((seed+n*11)%19)*200)})
			}
			history = append(history, m)
			history = append(history, results...)
		}
		history = append(history, llm.Message{Role: llm.RoleAssistant, Content: "done"}, llm.Message{Role: llm.RoleUser, Content: "continue"})
		before, _ := json.Marshal(history)
		got := omitResolvedToolHistory(history)
		assertEvidencePairs(t, got)
		cost := 0
		for _, m := range got {
			if m.Role == llm.RoleTool || len(m.ToolCalls) > 0 {
				cost += toolEvidenceBytes(m)
			}
		}
		if cost > recentToolEvidenceBytes || cost == 0 {
			t.Fatalf("seed=%d cost=%d", seed, cost)
		}
		after, _ := json.Marshal(history)
		if string(before) != string(after) {
			t.Fatal("canonical history changed")
		}
		if !reflect.DeepEqual(got, omitResolvedToolHistory(got)) {
			t.Fatalf("unstable projection seed=%d", seed)
		}
	}
}

func TestRecentMixedBatchProtectsNativeStateAndIncompleteCalls(t *testing.T) {
	for _, kind := range []string{"native", "active", "missing result", "duplicate result", "duplicate ID", "image result", "large assistant"} {
		t.Run(kind, func(t *testing.T) {
			m := mixedEvidenceHistory()
			switch kind {
			case "native":
				m[1].Parts = []llm.ContentPart{{Type: llm.PartTypeReasoning, Reasoning: &llm.ReasoningBlock{Format: llm.ReasoningResponses, Model: "test", Scope: "test", Data: json.RawMessage(`{"type":"reasoning","encrypted_content":"opaque"}`)}}}
			case "active":
				m = m[:4]
			case "missing result":
				m = append(m[:2], m[3:]...)
			case "duplicate result":
				m[2] = m[3]
			case "duplicate ID":
				m[1].ToolCalls[0].ID = "small"
			case "image result":
				m[3].Parts = []llm.ContentPart{{Type: llm.PartTypeImage, Image: &llm.ImageRef{MediaType: "image/png", Data: "AA=="}}}
			case "large assistant":
				m[1].Content = strings.Repeat("text", 1100)
			}
			got := omitResolvedToolHistory(m)
			if kind == "native" || kind == "active" {
				if !reflect.DeepEqual(got, m) {
					t.Fatalf("changed %s", kind)
				}
			} else {
				for _, message := range got {
					if len(message.ToolCalls) > 0 {
						t.Fatalf("replayed unsafe/overbudget batch: %s", kind)
					}
				}
			}
		})
	}
}

func TestRecentMixedBatchReachesBothModelRoutes(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprint(thin), func(t *testing.T) {
			reg := tools.NewRegistry()
			for _, name := range []string{"read_lines", "search_history"} {
				reg.MustRegister(tools.Tool{Name: name, Description: "read", ReadOnly: true, Schema: `{"type":"object"}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
					t.Fatal("unexpected retrieval")
					return tools.Result{}, nil
				}})
				reg.MarkAlwaysOn(name)
			}
			p := &stubProvider{name: "history", scripts: [][]llm.Delta{{{Content: "235 and 7.", FinishReason: "stop"}}}}
			input := mixedEvidenceHistory()
			loop, err := NewLoop(LoopConfig{Provider: p, Registry: reg, ThinTools: thin, Writer: &recordingWriter{}, InitialMessages: input[:len(input)-1]})
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range drainEvents(t, mustRun(t, loop, input[len(input)-1].Content)) {
				if failure, ok := event.(ErrorEvent); ok {
					t.Fatal(failure.Err)
				}
			}
			found := false
			for _, m := range p.reqs[0] {
				if m.Role == llm.RoleTool && m.ToolCallID == "small" {
					found = strings.Contains(m.Content, "RetryDelay=235")
				}
				if m.Role == llm.RoleTool && m.ToolCallID == "large" {
					t.Fatal("large result replayed")
				}
			}
			if !found {
				t.Fatal("follow-up lost small evidence")
			}
			assertEvidencePairs(t, p.reqs[0])
		})
	}
}
