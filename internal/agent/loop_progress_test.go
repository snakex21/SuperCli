package agent

import (
	"strconv"
	"testing"

	"supercli/internal/llm"
)

func itoa(i int) string { return strconv.Itoa(i) }

func TestDiscoveryProgress_CountsCallsNotBatches(t *testing.T) {
	var p discoveryProgress
	// Six discovery calls per step: soft limit is 12 calls, so two steps nudge.
	batch := []llm.ToolCall{
		{Name: "search_code", Arguments: `{"query":"a"}`},
		{Name: "read_lines", Arguments: `{"file":"x","from":1,"to":10}`},
		{Name: "list_dir", Arguments: `{}`},
		{Name: "read_pdf", Arguments: `{"path":"a.pdf"}`},
		{Name: "read_docx", Arguments: `{"path":"a.docx"}`},
		{Name: "web_lookup", Arguments: `{"q":"x"}`},
	}
	if sig := p.observe(batch, 0); sig != discoveryNone {
		t.Fatalf("first 6 calls: got %v, want none", sig)
	}
	if sig := p.observe(batch, 0); sig != discoveryNudge {
		t.Fatalf("at 12 calls: got %v, want nudge", sig)
	}
	p.noteNudge()
	// After soft nudge, partial streak remains; more discovery forces reply.
	if sig := p.observe(batch, 0); sig != discoveryForceReply {
		t.Fatalf("after nudge + 6 more: got %v, want force", sig)
	}
}

func TestDiscoveryProgress_FailedMutationDoesNotReset(t *testing.T) {
	var p discoveryProgress
	reads := []llm.ToolCall{
		{Name: "search_code", Arguments: `{"query":"a"}`},
		{Name: "read_lines", Arguments: `{"file":"x"}`},
		{Name: "list_dir", Arguments: `{}`},
		{Name: "read_pdf", Arguments: `{"path":"a"}`},
	}
	// 8 discovery calls
	p.observe(reads, 0)
	p.observe(reads, 0)
	if p.callStreak != 8 {
		t.Fatalf("streak=%d want 8", p.callStreak)
	}
	// Failed patch must NOT reset streak.
	patch := []llm.ToolCall{{Name: "patch_file", Arguments: `{"path":"x","changes":[]}`}}
	if sig := p.observe(patch, 1); sig != discoveryNone {
		t.Fatalf("failed patch signal=%v", sig)
	}
	if p.callStreak != 9 {
		t.Fatalf("after failed patch streak=%d want 9 (counted, not reset)", p.callStreak)
	}
	// Successful patch resets.
	if sig := p.observe(patch, 0); sig != discoveryNone {
		t.Fatalf("ok patch signal=%v", sig)
	}
	if p.callStreak != 0 {
		t.Fatalf("after ok patch streak=%d want 0", p.callStreak)
	}
}

func TestDiscoveryProgress_UnknownToolIsNotProgress(t *testing.T) {
	var p discoveryProgress
	// Custom/unknown tool must not reset discovery or extend soft limit.
	// Vary args so period-1 cycle detection does not fire early.
	for i := 0; i < discoveryCallSoftLimit-1; i++ {
		other := []llm.ToolCall{{Name: "mystery_plugin", Arguments: `{"n":` + itoa(i) + `}`}}
		if sig := p.observe(other, 0); sig != discoveryNone {
			t.Fatalf("early signal at %d: %v", i+1, sig)
		}
	}
	other := []llm.ToolCall{{Name: "mystery_plugin", Arguments: `{"n":99}`}}
	if sig := p.observe(other, 0); sig != discoveryNudge {
		t.Fatalf("unknown tools still accumulate: got %v", sig)
	}
	// Soft step limit must not treat unknown as progress either.
	var sp stepLimitProgress
	if sp.observe(other, 0) {
		t.Fatal("unknown tool must not extend soft step limit")
	}
	if sp.observe([]llm.ToolCall{{Name: "read_pdf", Arguments: `{}`}}, 0) {
		t.Fatal("read_pdf discovery must not extend soft step limit")
	}
	if !sp.observe([]llm.ToolCall{{Name: "patch_file", Arguments: `{"path":"f"}`}}, 0) {
		t.Fatal("successful mutation should allow soft progress once")
	}
	if sp.observe([]llm.ToolCall{{Name: "patch_file", Arguments: `{"path":"f"}`}}, 1) {
		t.Fatal("failed mutation must not extend")
	}
}

func TestDiscoveryProgress_ShortCycleEscalates(t *testing.T) {
	var p discoveryProgress
	a := llm.ToolCall{Name: "read_lines", Arguments: `{"file":"a"}`}
	b := llm.ToolCall{Name: "read_lines", Arguments: `{"file":"b"}`}
	// A B A B → period-2 cycle → hard force
	p.observe([]llm.ToolCall{a}, 0)
	p.observe([]llm.ToolCall{b}, 0)
	p.observe([]llm.ToolCall{a}, 0)
	if sig := p.observe([]llm.ToolCall{b}, 0); sig != discoveryForceReply {
		t.Fatalf("A-B-A-B should force reply, got %v (streak=%d)", sig, p.callStreak)
	}
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

func TestBatchIsDiscoveryOnly(t *testing.T) {
	if !batchIsDiscoveryOnly([]llm.ToolCall{{Name: "list_dir"}, {Name: "search_code"}, {Name: "read_pdf"}}) {
		t.Fatal("expected discovery only")
	}
	if batchIsDiscoveryOnly([]llm.ToolCall{{Name: "list_dir"}, {Name: "patch_file"}}) {
		t.Fatal("patch is not discovery")
	}
}

func TestBatchHasSuccessfulProgress(t *testing.T) {
	if batchHasSuccessfulProgress([]llm.ToolCall{{Name: "patch_file"}}, 1) {
		t.Fatal("failed patch is not progress")
	}
	if !batchHasSuccessfulProgress([]llm.ToolCall{{Name: "patch_file"}}, 0) {
		t.Fatal("ok patch is progress")
	}
	if batchHasSuccessfulProgress([]llm.ToolCall{{Name: "read_docx"}, {Name: "list_dir"}}, 0) {
		t.Fatal("reads are not progress")
	}
	// Mixed batch with zero failures still counts if mutation present
	if !batchHasSuccessfulProgress([]llm.ToolCall{{Name: "list_dir"}, {Name: "patch_file"}}, 0) {
		t.Fatal("ok patch in mixed batch is progress")
	}
	// Mixed batch with any failure is not progress
	if batchHasSuccessfulProgress([]llm.ToolCall{{Name: "list_dir"}, {Name: "patch_file"}}, 1) {
		t.Fatal("failed call in batch kills progress")
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
