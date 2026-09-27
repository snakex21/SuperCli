package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
)

// Direct embedders can still request share_context. It must inherit the current
// context view, not bring back the archive that was hidden to reduce prefill.
func TestWorkerSharedContextDoesNotRestoreHiddenHistory(t *testing.T) {
	reg := NewSubAgentRegistry()
	reg.MustRegister(SubAgent{Name: "explore", Description: "inspect"})
	parent := &Loop{Messages: []llm.Message{
		{Role: llm.RoleSystem, Content: "parent system"},
		{Role: llm.RoleUser, Content: "old request"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "old", Name: "read_lines", Arguments: "{}"}}},
		{Role: llm.RoleTool, ToolCallID: "old", Name: "read_lines", Content: strings.Repeat("OLD_ARCHIVE ", 20000)},
		{Role: llm.RoleUser, Content: "current request"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "current", Name: "read_lines", Arguments: "{}"}}},
		{Role: llm.RoleTool, ToolCallID: "current", Name: "read_lines", Content: "verified current evidence"},
	}}
	if err := parent.HideRange(1, 4); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(parent.Messages)
	var seed []llm.Message
	at, err := NewAgentTool(reg, parent, newTestBaseRegistry(), &stubReplyProvider{name: "fixture"}, nil, func(cfg LoopConfig) (*Loop, error) {
		seed = cfg.InitialMessages
		return childLoopFactory("done", nil)(cfg)
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := at.execute(context.Background(), json.RawMessage("{\"agent\":\"explore\",\"prompt\":\"inspect\",\"share_context\":true}"))
	if err != nil || result.Err != nil {
		t.Fatalf("task: %v %v", err, result.Err)
	}
	for _, message := range seed {
		if message.Role == llm.RoleSystem || strings.Contains(message.Content, "OLD_ARCHIVE") {
			t.Fatal("worker received hidden history or the parent system")
		}
	}
	if len(seed) != 4 || !strings.Contains(seed[0].Content, "3 message(s) compacted") ||
		!reflect.DeepEqual(seed[1:], parent.Messages[4:]) {
		t.Fatalf("current request or completed tool pair changed: %+v", seed)
	}
	after, _ := json.Marshal(parent.Messages)
	if string(before) != string(after) {
		t.Fatal("sharing rewrote the parent's archive")
	}
	t.Logf("estimated seed tokens: archive=%d, visible=%d", llm.EstimateTokens(parent.Messages[1:]), llm.EstimateTokens(seed))
}
