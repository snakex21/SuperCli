package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/files"
)

// patchArgs mimics what the model actually sends: the identical logical edit,
// carrying a fresh base_hash each time because it re-read the file (or echoed
// the previous call's after_hash) in between.
func patchArgs(baseHash string) string {
	return fmt.Sprintf(
		`{"path":"scripts.js","base_hash":%q,"changes":[{"old":"const x = 1;","new":"const x = 1;\n// helper for the modal","expected_count":1}]}`,
		baseHash)
}

// The production signature: 23 byte-identical, SUCCESSFUL patch_file calls that
// differed only in base_hash. Every repeat detector was blind to them.
func TestFingerprintIgnoresBaseHash(t *testing.T) {
	a := toolCallFingerprint("patch_file", patchArgs("aaaa"))
	b := toolCallFingerprint("patch_file", patchArgs("bbbb"))
	if a != b {
		t.Fatal("calls differing only in base_hash must share a fingerprint")
	}
	if c := toolCallFingerprint("patch_file", patchArgs("")); c != a {
		t.Fatal("an omitted base_hash must not change the fingerprint either")
	}
	// The edit itself still has to matter.
	other := `{"path":"scripts.js","base_hash":"aaaa","changes":[{"old":"const y = 2;","new":"const y = 3;","expected_count":1}]}`
	if toolCallFingerprint("patch_file", other) == a {
		t.Fatal("a different edit must not collapse onto the same fingerprint")
	}
	// base_hash is only advisory at the top level; nested content is data.
	nested := `{"path":"a","changes":[{"old":"x","new":"y","base_hash":"aaaa"}]}`
	nested2 := `{"path":"a","changes":[{"old":"x","new":"y","base_hash":"bbbb"}]}`
	if toolCallFingerprint("patch_file", nested) == toolCallFingerprint("patch_file", nested2) {
		t.Fatal("only the top-level advisory field may be dropped")
	}
}

func TestIdenticalSuccessGate_StopsRepeatedMutation(t *testing.T) {
	var g identicalSuccessGate
	for i := 0; i < repeatedMutationLimit; i++ {
		if g.shouldBlock("patch_file", patchArgs(fmt.Sprintf("hash%d", i))) {
			t.Fatalf("blocked after only %d successful applications", i)
		}
		g.recordSuccess("patch_file", patchArgs(fmt.Sprintf("hash%d", i)))
	}
	if !g.shouldBlock("patch_file", patchArgs("hash-fresh")) {
		t.Fatalf("the same successful edit must be refused after %d applications", repeatedMutationLimit)
	}
}

// Regression: repeating a read is ordinary work (the file changed, the listing
// is stale). The gate must never touch read-only tools.
func TestIdenticalSuccessGate_AllowsRepeatedReads(t *testing.T) {
	var g identicalSuccessGate
	args := `{"path":"scripts.js","from":1,"to":40}`
	for i := 0; i < 10; i++ {
		if g.shouldBlock("read_lines", args) {
			t.Fatalf("read_lines blocked on repeat %d", i+1)
		}
		g.recordSuccess("read_lines", args)
	}
	for _, name := range []string{"list_dir", "search_code", "read_many", "ctx_execute"} {
		g.recordSuccess(name, `{"a":1}`)
		g.recordSuccess(name, `{"a":1}`)
		g.recordSuccess(name, `{"a":1}`)
		if g.shouldBlock(name, `{"a":1}`) {
			t.Fatalf("%s is not a mutation and must not be gated", name)
		}
	}
}

// Regression: a session making several legitimate, different edits must run
// untouched — including many edits to the same file.
func TestIdenticalSuccessGate_AllowsDistinctEdits(t *testing.T) {
	var g identicalSuccessGate
	for i := 0; i < 30; i++ {
		args := fmt.Sprintf(
			`{"path":"scripts.js","changes":[{"old":"line %d","new":"line %d patched"}]}`, i, i)
		if g.shouldBlock("patch_file", args) {
			t.Fatalf("distinct edit %d was blocked", i)
		}
		g.recordSuccess("patch_file", args)
	}
	// Different files, identical change text: still distinct work.
	for i := 0; i < 5; i++ {
		args := fmt.Sprintf(`{"path":"mod%d.js","changes":[{"old":"a","new":"b"}]}`, i)
		if g.shouldBlock("patch_file", args) {
			t.Fatalf("edit to mod%d.js was blocked", i)
		}
		g.recordSuccess("patch_file", args)
	}
}

// The loop must not hand extra step budget to a batch it has already paid for,
// which is how the 23-patch run grew its budget from 25 to 30 steps.
func TestStepLimitProgress_RepeatEarnsNoBudget(t *testing.T) {
	var sp stepLimitProgress
	batch := func(hash string) []llm.ToolCall {
		return []llm.ToolCall{{Name: "patch_file", Arguments: patchArgs(hash)}}
	}
	if !sp.observe(batch("h1"), allOK(1)) {
		t.Fatal("first successful mutation should extend the budget")
	}
	for i := 0; i < 20; i++ {
		if sp.observe(batch(fmt.Sprintf("h%d", i+2)), allOK(1)) {
			t.Fatalf("repeat %d bought more step budget", i+1)
		}
	}
	// An A-B-A-B alternation must not collect a bonus on every step either.
	a := []llm.ToolCall{{Name: "patch_file", Arguments: `{"path":"a.js","changes":[{"old":"1","new":"2"}]}`}}
	b := []llm.ToolCall{{Name: "patch_file", Arguments: `{"path":"b.js","changes":[{"old":"1","new":"2"}]}`}}
	if !sp.observe(a, allOK(1)) || !sp.observe(b, allOK(1)) {
		t.Fatal("two genuinely new batches should each extend once")
	}
	if sp.observe(a, allOK(1)) || sp.observe(b, allOK(1)) {
		t.Fatal("A-B-A-B must not keep buying budget")
	}
}

// An edit that only duplicated text the file already had is a write, not
// progress: it must neither extend the budget nor reset the discovery streak.
func TestInertMutationIsNotProgress(t *testing.T) {
	calls := []llm.ToolCall{{Name: "patch_file", Arguments: patchArgs("h1")}}
	if batchHasSuccessfulProgress(calls, verdicts("i")) {
		t.Fatal("a duplicate-only patch must not count as progress")
	}
	if !batchHasSuccessfulProgress(calls, allOK(1)) {
		t.Fatal("a real patch must still count as progress")
	}
	mixed := []llm.ToolCall{
		{Name: "patch_file", Arguments: patchArgs("h1")},
		{Name: "patch_file", Arguments: `{"path":"b.js","changes":[{"old":"1","new":"2"}]}`},
	}
	if !batchHasSuccessfulProgress(mixed, verdicts("io")) {
		t.Fatal("one inert call must not erase a productive sibling")
	}

	var sp stepLimitProgress
	if sp.observe(calls, verdicts("i")) {
		t.Fatal("inert mutation must not extend the step budget")
	}
	var dp discoveryProgress
	dp.callStreak = 9
	if sig := dp.observe(calls, verdicts("i")); sig != discoveryNone {
		t.Fatalf("unexpected signal %v", sig)
	}
	if dp.callStreak == 0 {
		t.Fatal("inert mutation must not reset the discovery streak")
	}
}

// The full production scenario end-to-end, on a real file with the real
// patch_file tool: a pure insertion whose anchor survives the edit, so the
// identical patch keeps SUCCEEDING and keeps appending the same comment. This
// ran 23 times before the fix.
func TestInvoke_BlocksRepeatedSuccessfulPatch(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "scripts.js")
	if err := os.WriteFile(target, []byte("const x = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	reg.MustRegister(files.NewPatchFile(dir).Spec())
	loop, err := NewLoop(LoopConfig{Provider: echoProvider("ok"), Registry: reg, BaseDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	out := make(chan Event, 512)

	// The model refreshes base_hash every round, exactly as it did in the
	// session — the only thing that ever differed between the 23 calls.
	freshHash := func() string {
		data, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("%x", sha256.Sum256(data))
	}

	var last toolResult
	blockedAt := -1
	for i := 0; i < 8; i++ {
		last = loop.invoke(context.Background(), llm.ToolCall{
			ID:        fmt.Sprintf("c%d", i),
			Name:      "patch_file",
			Arguments: patchArgs(freshHash()),
		}, out)
		if last.failed && blockedAt < 0 {
			blockedAt = i
		}
	}
	if blockedAt != repeatedMutationLimit {
		t.Fatalf("gate closed at call %d, want %d", blockedAt, repeatedMutationLimit)
	}
	if len(last.followUps) == 0 || !strings.Contains(last.followUps[0].Content, "already succeeded") {
		t.Fatalf("model must be told why: %+v", last.followUps)
	}
	final, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(final), "// helper for the modal"); n != repeatedMutationLimit {
		t.Fatalf("comment appended %d times, want %d", n, repeatedMutationLimit)
	}
}

// The second, still-permitted application must already carry the one fact the
// model was missing: the inserted text is now in the file more than once.
func TestInvoke_RepeatedInsertionIsReportedAndNotProgress(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "scripts.js")
	if err := os.WriteFile(target, []byte("const x = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	reg.MustRegister(files.NewPatchFile(dir).Spec())
	loop, err := NewLoop(LoopConfig{Provider: echoProvider("ok"), Registry: reg, BaseDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	out := make(chan Event, 512)
	call := func() toolResult {
		data, _ := os.ReadFile(target)
		return loop.invoke(context.Background(), llm.ToolCall{
			ID: "x", Name: "patch_file",
			Arguments: patchArgs(fmt.Sprintf("%x", sha256.Sum256(data))),
		}, out)
	}
	first := call()
	if first.failed || first.inert {
		t.Fatalf("first patch should be a plain success: %+v", first)
	}
	if strings.Contains(first.followUps[0].Content, "pure insertion") {
		t.Fatal("an ordinary one-off patch must stay silent")
	}
	second := call()
	if second.failed {
		t.Fatal("the second patch still succeeds; only the report changes")
	}
	if !strings.Contains(second.followUps[0].Content, "occurs 2 times") {
		t.Fatalf("duplicate insertion must be named: %q", second.followUps[0].Content)
	}
	if !second.inert {
		t.Fatal("a patch that only duplicated existing text must not count as progress")
	}
}

// Regression: reads repeat freely through the same code path.
func TestInvoke_RepeatedReadsAreNotBlocked(t *testing.T) {
	runs := 0
	fn := func(_ context.Context, _ json.RawMessage) (tools.Result, error) {
		runs++
		return tools.Result{Text: "1: const x = 1;"}, nil
	}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "read_lines", Description: "read", Schema: `{}`, ReadOnly: true, Fn: fn})
	loop, err := NewLoop(LoopConfig{Provider: echoProvider("ok"), Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	out := make(chan Event, 256)
	for i := 0; i < 6; i++ {
		r := loop.invoke(context.Background(), llm.ToolCall{
			ID: fmt.Sprintf("r%d", i), Name: "read_lines", Arguments: `{"path":"a","from":1,"to":9}`,
		}, out)
		if r.failed {
			t.Fatalf("identical read %d was blocked", i+1)
		}
	}
	if runs != 6 {
		t.Fatalf("reads reaching the tool = %d, want 6", runs)
	}
}
