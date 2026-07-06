package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
)

// scriptedVerdictProvider returns a fixed reply and counts calls, so tests can
// assert the verdict model was (or was not) consulted.
type scriptedVerdictProvider struct {
	name  string
	reply string
	calls atomic.Int32
}

func (p *scriptedVerdictProvider) Name() string { return p.name }
func (p *scriptedVerdictProvider) Complete(ctx context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	p.calls.Add(1)
	ch := make(chan llm.Delta, 2)
	go func() {
		defer close(ch)
		ch <- llm.Delta{Content: p.reply}
		ch <- llm.Delta{FinishReason: "stop", Usage: &llm.Usage{Input: 40, Output: 6, Total: 46}}
	}()
	return ch, nil
}

// --- parseVerdict: robustness (CoerceArgs philosophy) ---

func TestParseVerdict(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    verdictKind
		wantOK  bool
		wantIns string
	}{
		{"strict accept", `{"decision":"accept"}`, verdictAccept, true, ""},
		{"strict revise", `{"decision":"revise","instruction":"fix foo.go line 3"}`, verdictRevise, true, "fix foo.go line 3"},
		{"strict takeover", `{"decision":"takeover"}`, verdictTakeover, true, ""},
		{"fenced json", "```json\n{\"decision\":\"accept\"}\n```", verdictAccept, true, ""},
		{"prose accept", "I ACCEPT this change, it looks good.", verdictAccept, true, ""},
		{"prose reject maps to revise", "reject: needs a test", verdictRevise, true, ""},
		{"garbage falls back to takeover", "asdf qwerty 12345", verdictTakeover, false, ""},
		{"empty falls back to takeover", "", verdictTakeover, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, ok := parseVerdict(c.raw)
			if v.Kind != c.want {
				t.Errorf("kind = %v, want %v", v.Kind, c.want)
			}
			if ok != c.wantOK {
				t.Errorf("ok = %v, want %v", ok, c.wantOK)
			}
			if c.wantIns != "" && !strings.Contains(v.Instruction, c.wantIns) {
				t.Errorf("instruction = %q, want contains %q", v.Instruction, c.wantIns)
			}
		})
	}
}

// --- runSieve: green/red/skipped ---

func TestRunSieve(t *testing.T) {
	ctx := context.Background()

	// No commands: skipped + green.
	cfg := &DraftVerifyConfig{}
	if r := cfg.runSieve(ctx, "."); !r.Green || !r.Skipped {
		t.Errorf("empty sieve = %+v, want green+skipped", r)
	}

	// Two green commands.
	green := &DraftVerifyConfig{
		VerifyCommands: []string{"a", "b"},
		runCommand: func(_ context.Context, _, _ string, _ time.Duration) (int, string) {
			return 0, "ok"
		},
	}
	if r := green.runSieve(ctx, "."); !r.Green || r.Skipped {
		t.Errorf("green sieve = %+v, want green", r)
	}

	// First command red stops the sieve; its output is the evidence.
	var ran []string
	red := &DraftVerifyConfig{
		VerifyCommands: []string{"build", "test"},
		runCommand: func(_ context.Context, _, cmd string, _ time.Duration) (int, string) {
			ran = append(ran, cmd)
			if cmd == "build" {
				return 2, "compile error near line 9"
			}
			return 0, "ok"
		},
	}
	r := red.runSieve(ctx, ".")
	if r.Green {
		t.Errorf("red sieve reported green: %+v", r)
	}
	if r.Command != "build" || r.Exit != 2 || !strings.Contains(r.Output, "compile error") {
		t.Errorf("red sieve evidence wrong: %+v", r)
	}
	if len(ran) != 1 {
		t.Errorf("sieve ran %v, want it to stop after the first red", ran)
	}
}

// --- Ladder integration through execute ---

// draftVerifyTool builds an AgentTool wired for draft-verify with a scripted
// worker reply and injectable sieve/diff/verdict, using childLoopFactory so no
// real LLM or files are touched.
func draftVerifyTool(t *testing.T, workerReply string, dv *DraftVerifyConfig) (*AgentTool, *atomic.Int32) {
	t.Helper()
	reg := NewSubAgentRegistry()
	MustRegisterAll(reg, BuiltinSubAgents())
	var calls atomic.Int32
	at, err := NewAgentTool(reg, nil, newTestBaseRegistry(),
		&stubReplyProvider{name: "coord"}, nil, childLoopFactory(workerReply, &calls))
	if err != nil {
		t.Fatalf("NewAgentTool: %v", err)
	}
	at.DraftVerify = dv
	return at, &calls
}

func call(t *testing.T, at *AgentTool, args map[string]any) string {
	t.Helper()
	raw, _ := json.Marshal(args)
	res, err := at.execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return res.Text
}

// OFF path must be byte-identical to a tool with no DraftVerify at all.
func TestDraftVerify_OffIsByteIdentical(t *testing.T) {
	atOff, _ := draftVerifyTool(t, "draft done", nil)
	atDisabled, _ := draftVerifyTool(t, "draft done", &DraftVerifyConfig{Enabled: false})

	args := map[string]any{"prompt": "do a thing"}
	got1 := call(t, atOff, args)
	got2 := call(t, atDisabled, args)
	if got1 != got2 {
		t.Fatalf("nil vs disabled differ:\n%q\n%q", got1, got2)
	}
	if !strings.Contains(got1, "draft done") {
		t.Errorf("off path lost the worker report: %q", got1)
	}
	if strings.Contains(got1, "draft-verify") {
		t.Errorf("off path leaked ladder text: %q", got1)
	}
}

// Green sieve + ACCEPT verdict returns the draft and consults the verdict model.
func TestDraftVerify_AcceptReturnsDraft(t *testing.T) {
	vp := &scriptedVerdictProvider{name: "big", reply: `{"decision":"accept"}`}
	dv := &DraftVerifyConfig{
		Enabled:        true,
		VerifyCommands: []string{"go build"},
		Verdict:        vp,
		runCommand:     func(_ context.Context, _, _ string, _ time.Duration) (int, string) { return 0, "ok" },
		gitDiff:        func(_ context.Context, _ string) string { return "diff --git a b" },
	}
	at, _ := draftVerifyTool(t, "implemented feature X", dv)
	got := call(t, at, map[string]any{"prompt": "add feature X"})
	if vp.calls.Load() != 1 {
		t.Errorf("verdict model calls = %d, want 1", vp.calls.Load())
	}
	if !strings.Contains(got, "implemented feature X") {
		t.Errorf("accept did not return the draft: %q", got)
	}
	if strings.Contains(got, "NOT auto-accepted") {
		t.Errorf("accept path emitted a handback: %q", got)
	}
}

// REVISE runs one worker round then accepts; the worker loop is re-entered.
func TestDraftVerify_ReviseThenAccept(t *testing.T) {
	// Verdict says revise first, accept second.
	vp := &revisingProvider{replies: []string{
		`{"decision":"revise","instruction":"add the missing test"}`,
		`{"decision":"accept"}`,
	}}
	sieveExit := int32(2) // red first, then flip to green
	dv := &DraftVerifyConfig{
		Enabled:        true,
		VerifyCommands: []string{"go test"},
		Verdict:        vp,
		runCommand: func(_ context.Context, _, _ string, _ time.Duration) (int, string) {
			if atomic.LoadInt32(&sieveExit) != 0 {
				atomic.StoreInt32(&sieveExit, 0)
				return 2, "FAIL: missing test"
			}
			return 0, "ok"
		},
		gitDiff: func(_ context.Context, _ string) string { return "diff" },
	}
	at, workerCalls := draftVerifyTool(t, "did the work", dv)
	got := call(t, at, map[string]any{"prompt": "add feature with test"})
	// One initial draft + one revise round = 2 worker loop constructions is
	// wrong (factory builds once); worker is REUSED via runWorkerLoop, so we
	// assert the verdict was consulted twice and the accept path won.
	if vp.calls.Load() != 2 {
		t.Errorf("verdict calls = %d, want 2 (revise then accept)", vp.calls.Load())
	}
	_ = workerCalls
	if strings.Contains(got, "NOT auto-accepted") {
		t.Errorf("revise-then-accept should end accepted, got handback: %q", got)
	}
}

// REVISE forever hits the round limit and hands back (does not loop).
func TestDraftVerify_RoundLimitEnforced(t *testing.T) {
	vp := &scriptedVerdictProvider{name: "big", reply: `{"decision":"revise","instruction":"still broken"}`}
	dv := &DraftVerifyConfig{
		Enabled:        true,
		MaxRounds:      2,
		VerifyCommands: []string{"go test"},
		Verdict:        vp,
		runCommand:     func(_ context.Context, _, _ string, _ time.Duration) (int, string) { return 1, "still failing" },
		gitDiff:        func(_ context.Context, _ string) string { return "diff" },
	}
	at, _ := draftVerifyTool(t, "attempt", dv)
	got := call(t, at, map[string]any{"prompt": "fix the bug"})
	// rounds 0 and 1 both REVISE (round < 2), round 2 is past the limit and
	// hands back → 3 verdict calls total.
	if vp.calls.Load() != 3 {
		t.Errorf("verdict calls = %d, want 3 (bounded)", vp.calls.Load())
	}
	if !strings.Contains(got, "NOT auto-accepted") {
		t.Errorf("round limit should hand back: %q", got)
	}
	if !strings.Contains(got, "still failing") {
		t.Errorf("handback should carry the sieve evidence: %q", got)
	}
}

// A corrupted verdict is a SAFE fallback (takeover handback), never a crash.
func TestDraftVerify_CorruptVerdictFallsBackToTakeover(t *testing.T) {
	vp := &scriptedVerdictProvider{name: "big", reply: "%%% not json at all %%%"}
	dv := &DraftVerifyConfig{
		Enabled:        true,
		VerifyCommands: []string{"go build"},
		Verdict:        vp,
		runCommand:     func(_ context.Context, _, _ string, _ time.Duration) (int, string) { return 0, "ok" },
		gitDiff:        func(_ context.Context, _ string) string { return "diff" },
	}
	at, _ := draftVerifyTool(t, "did stuff", dv)
	got := call(t, at, map[string]any{"prompt": "do stuff"})
	if !strings.Contains(got, "unparsed verdict") {
		t.Errorf("corrupt verdict should annotate as unparsed takeover: %q", got)
	}
	if !strings.Contains(got, "did stuff") {
		t.Errorf("handback should still carry the worker's report: %q", got)
	}
}

// --- Task B: advise is read-only and never enters the ladder ---

func TestAdvise_RoutesToAdvisorAndSkipsLadder(t *testing.T) {
	vp := &scriptedVerdictProvider{name: "big", reply: `{"decision":"accept"}`}
	sieveRuns := int32(0)
	dv := &DraftVerifyConfig{
		Enabled:        true,
		VerifyCommands: []string{"go build"},
		Verdict:        vp,
		runCommand: func(_ context.Context, _, _ string, _ time.Duration) (int, string) {
			atomic.AddInt32(&sieveRuns, 1)
			return 0, "ok"
		},
		gitDiff: func(_ context.Context, _ string) string { return "diff" },
	}
	at, _ := draftVerifyTool(t, "I recommend approach A", dv)
	got := call(t, at, map[string]any{"prompt": "A or B?", "advise": true})

	if atomic.LoadInt32(&sieveRuns) != 0 {
		t.Errorf("advise must not run the sieve, ran %d times", sieveRuns)
	}
	if vp.calls.Load() != 0 {
		t.Errorf("advise must not call the verdict model, called %d times", vp.calls.Load())
	}
	if !strings.Contains(got, "advisor") {
		t.Errorf("advise should route to the advisor worker: %q", got)
	}
	if !strings.Contains(got, "I recommend approach A") {
		t.Errorf("advise should return the opinion: %q", got)
	}
}

// advisor must be a registered, read-only builtin (no write/edit tools).
func TestAdvisorBuiltinIsReadOnly(t *testing.T) {
	reg := NewSubAgentRegistry()
	MustRegisterAll(reg, BuiltinSubAgents())
	spec, ok := reg.Get("advisor")
	if !ok {
		t.Fatal("advisor builtin not registered")
	}
	for _, tool := range spec.AllowedTools {
		low := strings.ToLower(tool)
		for _, banned := range []string{"write", "edit", "delete", "move", "trash", "make", "execute"} {
			if strings.Contains(low, banned) {
				t.Errorf("advisor has a mutating tool %q", tool)
			}
		}
	}
}

func TestDraftVerifyTelemetryLine(t *testing.T) {
	tel := draftVerifyTelemetry{Outcome: "ACCEPT", Rounds: 1, DraftSteps: 3,
		DraftTokIn: 500, DraftTokOut: 200, VerifyTokIn: 300, VerifyTokOut: 10, SieveRed: 1}
	line := tel.Line()
	for _, want := range []string{"draft-verify: ACCEPT", "1 round", "draft 3 steps 500/200", "verify 300/10", "1 red"} {
		if !strings.Contains(line, want) {
			t.Errorf("telemetry line %q missing %q", line, want)
		}
	}
}

// revisingProvider replies from a script, one entry per call, so a verdict
// sequence (revise → accept) can be tested.
type revisingProvider struct {
	replies []string
	calls   atomic.Int32
}

func (p *revisingProvider) Name() string { return "big" }
func (p *revisingProvider) Complete(ctx context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	n := int(p.calls.Add(1)) - 1
	reply := `{"decision":"accept"}`
	if n < len(p.replies) {
		reply = p.replies[n]
	}
	ch := make(chan llm.Delta, 2)
	go func() {
		defer close(ch)
		ch <- llm.Delta{Content: reply}
		ch <- llm.Delta{FinishReason: "stop", Usage: &llm.Usage{Input: 30, Output: 5, Total: 35}}
	}()
	return ch, nil
}
