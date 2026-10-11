package agent

import (
	"strings"
	"supercli/internal/checkpoint"
	"supercli/internal/llm"
	"testing"
)

func TestExternalCheckpointNoticeDoesNotChangeTransferDestination(t *testing.T) {
	call := llm.ToolCall{ID: "copy", Name: "copy", Arguments: `{"src":"source.txt","dest":"other/file.txt"}`}
	body := "Copied source.txt -> other/file.txt\n" + checkpoint.ExternalCheckpointNotice
	history := []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}}, {Role: llm.RoleTool, ToolCallID: call.ID, Name: call.Name, Content: body}}
	facts := CompactFacts(history, nil)
	if !strings.Contains(facts, "files_modified: other/file.txt") || strings.Contains(facts, "External file") {
		t.Fatalf("notice parsed as destination: %s", facts)
	}
}
