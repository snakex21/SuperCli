package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func tailPinFixture(t *testing.T, replace bool, live func() string) (*Loop, *stubProvider) {
	t.Helper()
	registry := tools.NewRegistry()
	registry.MustRegister(tools.Tool{Name: "fixture_read", Description: "Read fixture", ReadOnly: true, Schema: `{"type":"object"}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "fixture body"}, nil
		}})
	registry.MarkAlwaysOn("fixture_read")
	call := func(id string) []llm.Delta {
		return []llm.Delta{{ToolCall: &llm.ToolCall{ID: id, Name: "fixture_read", Arguments: `{"n":"` + id + `"}`}, FinishReason: "tool_calls"}}
	}
	provider := &stubProvider{name: "fixture", scripts: [][]llm.Delta{call("a"), call("b"), call("c"), {{Content: "done", FinishReason: "stop"}}}}
	cfg := LoopConfig{Provider: provider, Registry: registry, System: "Stable policy", ReplaceContextTail: replace}
	if live != nil {
		cfg.LiveContextBlocks = func(context.Context) ([]llm.ContextBlock, error) {
			return []llm.ContextBlock{{Source: llm.SourceMemory, Text: live()}}, nil
		}
	}
	loop, err := NewLoop(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return loop, provider
}

func tailCount(req []llm.Message) int {
	n := 0
	for _, m := range req[1:] {
		if m.Role == llm.RoleSystem && strings.Contains(m.Content, "Current local date/time:") {
			n++
		}
	}
	return n
}

// Every request of a run must start with the complete previous request, so a
// hosted prompt cache that only reuses previously sent prompts hits the whole
// history instead of only the system/tool block.
func TestTailPinKeepsRequestsAppendOnly(t *testing.T) {
	loop, provider := tailPinFixture(t, false, nil)
	drainEvents(t, mustRun(t, loop, "Read the fixture three times."))
	if len(provider.reqs) != 4 {
		t.Fatalf("requests=%d", len(provider.reqs))
	}
	for i, req := range provider.reqs {
		if tailCount(req) != 1 {
			t.Fatalf("request %d carries %d tails, want exactly 1", i+1, tailCount(req))
		}
		if i == 0 {
			continue
		}
		prev := provider.reqs[i-1]
		if len(req) <= len(prev) || !reflect.DeepEqual(prev, req[:len(prev)]) {
			t.Fatalf("request %d does not extend request %d", i+1, i)
		}
	}
	for _, m := range loop.AllMessages() {
		if strings.Contains(m.Content, "Current local date/time:") {
			t.Fatal("pinned tail leaked into canonical history")
		}
	}
}

func TestTailPinMovesWhenContentChanges(t *testing.T) {
	loop, _ := tailPinFixture(t, false, nil)
	loop.liveContext = "MEMORY_V1"
	loop.Messages = append(loop.Messages, llm.Message{Role: llm.RoleUser, Content: "task"})
	first, _ := loop.prepareProviderMessages(true)
	loop.Messages = append(loop.Messages,
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "a", Name: "fixture_read", Arguments: "{}"}}},
		llm.Message{Role: llm.RoleTool, ToolCallID: "a", Name: "fixture_read", Content: "body"})
	second, _ := loop.prepareProviderMessages(true)
	if !reflect.DeepEqual(first, second[:len(first)]) || second[len(second)-1].Role != llm.RoleTool {
		t.Fatal("unchanged tail did not stay pinned")
	}
	loop.liveContext = "MEMORY_V2"
	third, _ := loop.prepareProviderMessages(true)
	text := liveRequestText(third)
	if tailCount(third) != 1 || strings.Contains(text, "MEMORY_V1") || !strings.Contains(text, "MEMORY_V2") || third[len(third)-1].Role != llm.RoleSystem {
		t.Fatal("changed tail was not moved to the end as the only tail")
	}
	loop.Messages = append(loop.Messages, llm.Message{Role: llm.RoleAssistant, Content: "done"})
	fourth, _ := loop.prepareProviderMessages(true)
	if !reflect.DeepEqual(third, fourth[:len(third)]) {
		t.Fatal("request after the move does not extend the moved tail")
	}
}

func TestReplaceContextTailKeepsLegacyPlacement(t *testing.T) {
	loop, provider := tailPinFixture(t, true, nil)
	drainEvents(t, mustRun(t, loop, "Read the fixture three times."))
	for i, req := range provider.reqs {
		if last := req[len(req)-1]; last.Role != llm.RoleSystem || !strings.Contains(last.Content, "Current local date/time:") || tailCount(req) != 1 {
			t.Fatalf("request %d: legacy tail is not the single last message", i+1)
		}
	}
}

func TestTailPinDroppedWhenHistoryRewritten(t *testing.T) {
	loop, _ := tailPinFixture(t, false, nil)
	loop.Messages = append(loop.Messages, llm.Message{Role: llm.RoleUser, Content: "first"})
	if _, _ = loop.prepareProviderMessages(true); loop.tailPin == nil {
		t.Fatal("request did not pin its tail")
	}
	loop.Messages[len(loop.Messages)-1].Content = "rewritten"
	loop.invalidateVisibleEstimate()
	wire, _ := loop.prepareProviderMessages(true)
	if last := wire[len(wire)-1]; last.Role != llm.RoleSystem || tailCount(wire) != 1 {
		t.Fatal("rewritten history kept a stale pin position")
	}
}
