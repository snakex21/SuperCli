package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func call(id, name string) Message {
	return Message{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: id, Name: name, Arguments: "{}"}}}
}

func result(id string) Message {
	return Message{Role: RoleTool, ToolCallID: id, Content: "ok"}
}

// assertWellFormed checks the invariant every provider requires:
// unique tool-call ids, and a 1:1 pairing between calls and results.
func assertWellFormed(t *testing.T, msgs []Message) {
	t.Helper()
	seen := map[string]bool{}
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			if tc.ID == "" {
				t.Fatalf("empty tool call id")
			}
			if seen[tc.ID] {
				t.Fatalf("duplicate tool_call_id %q survived repair", tc.ID)
			}
			seen[tc.ID] = true
		}
	}
	for _, m := range msgs {
		if m.Role == RoleTool && !seen[m.ToolCallID] {
			t.Fatalf("orphan tool result %q survived repair", m.ToolCallID)
		}
	}
}

// The reported production failure: a long session where the same
// synthetic sentinel id was reused for every ctx_execute call, and
// the provider answered HTTP 400 "Duplicate value for
// 'tool_call_id' of sentinel_ctx_execute in message[73]".
func TestRepairToolCallIDs_DuplicateSentinel(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "go"},
		call("sentinel_ctx_execute", "ctx_execute"),
		result("sentinel_ctx_execute"),
		call("sentinel_ctx_execute", "ctx_execute"),
		result("sentinel_ctx_execute"),
		call("sentinel_ctx_execute", "ctx_execute"),
		result("sentinel_ctx_execute"),
	}
	out := repairToolCallIDs(msgs)
	assertWellFormed(t, out)
	if len(out) != len(msgs) {
		t.Fatalf("len = %d, want %d (no message may be lost)", len(out), len(msgs))
	}
	// Each result must still answer the call directly above it.
	for i := 1; i < len(out); i += 2 {
		if out[i].ToolCalls[0].ID != out[i+1].ToolCallID {
			t.Fatalf("pair %d broken: call %q vs result %q",
				i, out[i].ToolCalls[0].ID, out[i+1].ToolCallID)
		}
	}
}

// A saved session that already contains the duplicate must become
// sendable again — the repair happens on the way out, so loading an
// old broken conversation is enough to continue it.
func TestRepairToolCallIDs_SavedBrokenHistoryBecomesSendable(t *testing.T) {
	msgs := []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "hi"},
		call("sentinel_ctx_execute", "ctx_execute"),
		result("sentinel_ctx_execute"),
		call("sentinel_ctx_execute", "ctx_execute"),
		result("sentinel_ctx_execute"),
		{Role: RoleUser, Content: "continue"},
	}
	body, err := buildOpenAIRequest("m", msgs, nil, false, false)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var req struct {
		Messages []struct {
			ToolCallID string `json:"tool_call_id"`
			ToolCalls  []struct {
				ID string `json:"id"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	seen := map[string]bool{}
	for _, m := range req.Messages {
		for _, tc := range m.ToolCalls {
			if seen[tc.ID] {
				t.Fatalf("request carries duplicate tool_call_id %q", tc.ID)
			}
			seen[tc.ID] = true
		}
	}
	if len(seen) != 2 {
		t.Fatalf("got %d tool calls on the wire, want 2", len(seen))
	}
}

// An orphan tool result (its call was pruned/compacted away) is a
// 400 at several providers; it must be dropped.
func TestRepairToolCallIDs_OrphanResultDropped(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "hi"},
		result("gone"),
		{Role: RoleAssistant, Content: "done"},
	}
	out := repairToolCallIDs(msgs)
	assertWellFormed(t, out)
	for _, m := range out {
		if m.Role == RoleTool {
			t.Fatalf("orphan result kept: %+v", m)
		}
	}
}

// An assistant tool call that never got a result (run cancelled
// mid-execution, then resumed) is the mirror-image 400.
func TestRepairToolCallIDs_OrphanCallDropped(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "hi"},
		{Role: RoleAssistant, Content: "let me look", ToolCalls: []ToolCall{{ID: "a", Name: "read", Arguments: "{}"}}},
		{Role: RoleUser, Content: "never mind"},
	}
	out := repairToolCallIDs(msgs)
	assertWellFormed(t, out)
	if len(out) != 3 {
		t.Fatalf("len = %d, want 3 (assistant prose is kept)", len(out))
	}
	if len(out[1].ToolCalls) != 0 {
		t.Fatalf("orphan call kept: %+v", out[1].ToolCalls)
	}
}

// An assistant message that is nothing BUT an unanswered tool call
// has nothing left to send and goes away entirely.
func TestRepairToolCallIDs_EmptyAssistantDropped(t *testing.T) {
	out := repairToolCallIDs([]Message{
		{Role: RoleUser, Content: "hi"},
		call("a", "read"),
	})
	assertWellFormed(t, out)
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1", len(out))
	}
}

// The healthy path must be a no-op, and must not copy.
func TestRepairToolCallIDs_HealthyUntouched(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "hi"},
		call("a", "read"),
		result("a"),
		call("b", "read"),
		result("b"),
	}
	out := repairToolCallIDs(msgs)
	if len(out) != len(msgs) {
		t.Fatalf("len = %d, want %d", len(out), len(msgs))
	}
	if &out[0] != &msgs[0] {
		t.Errorf("healthy history was copied; repair should be a no-op")
	}
}

// Synthetic ids minted in different runs of the same process must
// not collide once the two histories are concatenated (resume,
// rewind, prune all splice runs together).
func TestRepairToolCallIDs_ManyDuplicatesAllUnique(t *testing.T) {
	var msgs []Message
	for i := 0; i < 40; i++ {
		msgs = append(msgs, call("sentinel_ctx_execute", "ctx_execute"), result("sentinel_ctx_execute"))
	}
	out := repairToolCallIDs(msgs)
	assertWellFormed(t, out)
	if len(out) != 80 {
		t.Fatalf("len = %d, want 80", len(out))
	}
	for _, m := range out {
		for _, tc := range m.ToolCalls {
			if !strings.HasPrefix(tc.ID, "sentinel_ctx_execute") {
				t.Fatalf("id %q lost its origin", tc.ID)
			}
		}
	}
}
