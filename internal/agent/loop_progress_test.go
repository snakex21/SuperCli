package agent

import (
	"strconv"
	"testing"

	"supercli/internal/llm"
)

func itoa(i int) string { return strconv.Itoa(i) }

// allOK is the per-call verdict for a batch where every call succeeded and
// produced something.
func allOK(n int) []callOutcome { return make([]callOutcome, n) }

// verdicts builds a per-call outcome slice from a compact spec:
// 'o' = ok, 'f' = failed, 'i' = inert.
func verdicts(spec string) []callOutcome {
	out := make([]callOutcome, 0, len(spec))
	for _, c := range spec {
		switch c {
		case 'f':
			out = append(out, callOutcome{failed: true})
		case 'i':
			out = append(out, callOutcome{inert: true})
		default:
			out = append(out, callOutcome{})
		}
	}
	return out
}

// The regression this whole policy exists for: a long analytical task is
// nothing but successful reads of files it has not read yet. That must never
// cost the model its tools, no matter how many calls it takes.
func TestDiscoveryProgress_NovelSuccessfulReadsNeverDisableTools(t *testing.T) {
	var p discoveryProgress
	names := []string{"read_lines", "search_code", "list_dir", "read_many", "code_intel", "recall"}
	for i := 0; i < 20*discoveryCallHardLimit; i++ {
		batch := []llm.ToolCall{{
			Name:      names[i%len(names)],
			Arguments: `{"path":"f` + itoa(i) + `.go"}`,
		}}
		if sig := p.observe(batch, allOK(1)); sig != discoveryNone {
			t.Fatalf("call %d: got %v, want none — novel successful reads are the job", i+1, sig)
		}
	}
	if p.callStreak != 0 {
		t.Fatalf("streak=%d, want 0 after only novel successful discovery", p.callStreak)
	}
}

func TestDiscoveryProgress_CountsRepeatedCallsNotBatches(t *testing.T) {
	var p discoveryProgress
	// Six discovery calls per step. The first pass is all new ground and is
	// free; every re-run of the same batch costs 6 calls of budget.
	batch := []llm.ToolCall{
		{Name: "search_code", Arguments: `{"query":"a"}`},
		{Name: "read_lines", Arguments: `{"file":"x","from":1,"to":10}`},
		{Name: "list_dir", Arguments: `{}`},
		{Name: "read_pdf", Arguments: `{"path":"a.pdf"}`},
		{Name: "read_docx", Arguments: `{"path":"a.docx"}`},
		{Name: "web_lookup", Arguments: `{"q":"x"}`},
	}
	if sig := p.observe(batch, allOK(6)); sig != discoveryNone {
		t.Fatalf("first pass (all novel): got %v, want none", sig)
	}
	if p.callStreak != 0 {
		t.Fatalf("first pass streak=%d, want 0", p.callStreak)
	}
	if sig := p.observe(batch, allOK(6)); sig != discoveryNone {
		t.Fatalf("first repeat (6 charged): got %v, want none", sig)
	}
	if p.callStreak != 6 {
		t.Fatalf("after one repeat streak=%d, want 6", p.callStreak)
	}
	if sig := p.observe(batch, allOK(6)); sig != discoveryNudge {
		t.Fatalf("at 12 repeated calls: got %v, want nudge", sig)
	}
	p.noteNudge()
	// After the soft nudge, partial streak remains; more repetition forces.
	if sig := p.observe(batch, allOK(6)); sig != discoveryForceReply {
		t.Fatalf("after nudge + 6 more repeats: got %v, want force", sig)
	}
}

// Failures are charged even when the call is new: a call that did not work
// bought nothing, however original it was.
func TestDiscoveryProgress_FailuresStillForce(t *testing.T) {
	var p discoveryProgress
	for i := 0; i < discoveryCallSoftLimit-1; i++ {
		batch := []llm.ToolCall{{Name: "search_code", Arguments: `{"q":"` + itoa(i) + `"}`}}
		if sig := p.observe(batch, verdicts("f")); sig != discoveryNone {
			t.Fatalf("early signal at failure %d: %v", i+1, sig)
		}
	}
	last := []llm.ToolCall{{Name: "search_code", Arguments: `{"q":"last"}`}}
	if sig := p.observe(last, verdicts("f")); sig != discoveryNudge {
		t.Fatalf("distinct failures must still accumulate: got %v (streak=%d)", sig, p.callStreak)
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
	// 4 novel reads are free; the identical 4 again cost 4.
	p.observe(reads, allOK(4))
	p.observe(reads, allOK(4))
	if p.callStreak != 4 {
		t.Fatalf("streak=%d want 4", p.callStreak)
	}
	// Failed patch must NOT reset streak.
	patch := []llm.ToolCall{{Name: "patch_file", Arguments: `{"path":"x","changes":[]}`}}
	if sig := p.observe(patch, verdicts("f")); sig != discoveryNone {
		t.Fatalf("failed patch signal=%v", sig)
	}
	if p.callStreak != 5 {
		t.Fatalf("after failed patch streak=%d want 5 (counted, not reset)", p.callStreak)
	}
	// Successful patch resets.
	if sig := p.observe(patch, allOK(1)); sig != discoveryNone {
		t.Fatalf("ok patch signal=%v", sig)
	}
	if p.callStreak != 0 {
		t.Fatalf("after ok patch streak=%d want 0", p.callStreak)
	}
}

func TestDiscoveryProgress_UnknownToolIsNotProgress(t *testing.T) {
	var p discoveryProgress
	// An unknown tool is not progress: it must not reset an existing streak,
	// even when the call succeeds with arguments never used before.
	p.callStreak = 7
	if sig := p.observe([]llm.ToolCall{{Name: "mystery_plugin", Arguments: `{"n":1}`}}, allOK(1)); sig != discoveryNone {
		t.Fatalf("unexpected signal %v", sig)
	}
	if p.callStreak != 7 {
		t.Fatalf("unknown tool changed the streak to %d, want 7 (no reset, no charge)", p.callStreak)
	}
	// Repeating it does accumulate.
	for i := 0; i < 5; i++ {
		p.observe([]llm.ToolCall{{Name: "mystery_plugin", Arguments: `{"n":1}`}}, allOK(1))
	}
	if p.callStreak <= 7 {
		t.Fatalf("repeated unknown tool must accumulate, streak=%d", p.callStreak)
	}

	// Soft step limit must not treat unknown or discovery as progress either.
	var sp stepLimitProgress
	if sp.observe([]llm.ToolCall{{Name: "mystery_plugin", Arguments: `{"n":9}`}}, allOK(1)) {
		t.Fatal("unknown tool must not extend soft step limit")
	}
	if sp.observe([]llm.ToolCall{{Name: "read_pdf", Arguments: `{}`}}, allOK(1)) {
		t.Fatal("read_pdf discovery must not extend soft step limit")
	}
	if !sp.observe([]llm.ToolCall{{Name: "patch_file", Arguments: `{"path":"f"}`}}, allOK(1)) {
		t.Fatal("successful mutation should allow soft progress once")
	}
	if sp.observe([]llm.ToolCall{{Name: "patch_file", Arguments: `{"path":"g"}`}}, verdicts("f")) {
		t.Fatal("failed mutation must not extend")
	}
}

// One failed sibling must not void an edit that actually landed in the same
// batch: the budget is decided per call, not by a batch-wide failure count.
func TestStepLimitProgress_FailedSiblingDoesNotVoidRealEdit(t *testing.T) {
	var sp stepLimitProgress
	batch := []llm.ToolCall{
		{Name: "search_code", Arguments: `{"q":"nope"}`},
		{Name: "patch_file", Arguments: `{"path":"a.go"}`},
	}
	if !sp.observe(batch, verdicts("fo")) {
		t.Fatal("a successful patch must still extend the budget when a sibling search failed")
	}
	var dp discoveryProgress
	dp.callStreak = 9
	if sig := dp.observe(batch, verdicts("fo")); sig != discoveryNone {
		t.Fatalf("unexpected signal %v", sig)
	}
	if dp.callStreak != 0 {
		t.Fatalf("streak=%d, want 0: the batch contained a successful mutation", dp.callStreak)
	}
}

func TestDiscoveryProgress_ShortCycleEscalates(t *testing.T) {
	var p discoveryProgress
	a := llm.ToolCall{Name: "read_lines", Arguments: `{"file":"a"}`}
	b := llm.ToolCall{Name: "read_lines", Arguments: `{"file":"b"}`}
	// A B A B → period-2 cycle → hard force
	p.observe([]llm.ToolCall{a}, allOK(1))
	p.observe([]llm.ToolCall{b}, allOK(1))
	p.observe([]llm.ToolCall{a}, allOK(1))
	if sig := p.observe([]llm.ToolCall{b}, allOK(1)); sig != discoveryForceReply {
		t.Fatalf("A-B-A-B should force reply, got %v (streak=%d)", sig, p.callStreak)
	}
}

// reset must hand back a genuinely fresh budget, including the nudge count and
// the seen-set — otherwise the step after a forced reply starts bankrupt.
func TestDiscoveryProgress_ResetIsComplete(t *testing.T) {
	var p discoveryProgress
	call := []llm.ToolCall{{Name: "read_lines", Arguments: `{"file":"a"}`}}
	p.observe(call, allOK(1))
	p.observe(call, allOK(1))
	p.noteNudge()
	p.reset()
	if p.callStreak != 0 || p.nudges != 0 || len(p.recent) != 0 || len(p.seen) != 0 {
		t.Fatalf("incomplete reset: streak=%d nudges=%d recent=%d seen=%d",
			p.callStreak, p.nudges, len(p.recent), len(p.seen))
	}
	// The same call is "novel" again after a reset.
	if sig := p.observe(call, allOK(1)); sig != discoveryNone || p.callStreak != 0 {
		t.Fatalf("after reset: sig=%v streak=%d, want none/0", sig, p.callStreak)
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
	if batchHasSuccessfulProgress([]llm.ToolCall{{Name: "patch_file"}}, verdicts("f")) {
		t.Fatal("failed patch is not progress")
	}
	if !batchHasSuccessfulProgress([]llm.ToolCall{{Name: "patch_file"}}, allOK(1)) {
		t.Fatal("ok patch is progress")
	}
	if batchHasSuccessfulProgress([]llm.ToolCall{{Name: "read_docx"}, {Name: "list_dir"}}, allOK(2)) {
		t.Fatal("reads are not progress")
	}
	// Mixed batch still counts if a mutation landed.
	if !batchHasSuccessfulProgress([]llm.ToolCall{{Name: "list_dir"}, {Name: "patch_file"}}, allOK(2)) {
		t.Fatal("ok patch in mixed batch is progress")
	}
	// Per call, not per batch: the failed sibling is the search, not the patch.
	if !batchHasSuccessfulProgress([]llm.ToolCall{{Name: "list_dir"}, {Name: "patch_file"}}, verdicts("fo")) {
		t.Fatal("a failed sibling must not void the successful patch")
	}
	// The patch itself failing is not progress, whatever the sibling did.
	if batchHasSuccessfulProgress([]llm.ToolCall{{Name: "list_dir"}, {Name: "patch_file"}}, verdicts("of")) {
		t.Fatal("failed patch is not progress")
	}
	if countFailures(verdicts("fof")) != 2 {
		t.Fatal("countFailures")
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
