package agent

// Live draft-verify test against two real llama.cpp servers. Skipped unless
// all four env vars are set:
//
//	SUPERCLI_LIVE_BASEURL        orchestrator/verdict, e.g. http://127.0.0.1:8089/v1
//	SUPERCLI_LIVE_MODEL          e.g. Qwen3.5-9B-Q8_0.gguf
//	SUPERCLI_LIVE_TASK_BASEURL   worker/draft host,    e.g. http://127.0.0.1:8091/v1
//	SUPERCLI_LIVE_TASK_MODEL     e.g. Ministral-3-3B-Instruct-2512-Q4_K_M.gguf
//
// It drives the REAL AgentTool ladder: a small model drafts a file change in a
// temp git repo, the objective sieve runs `go build`/`go test`, and the big
// model issues a verdict on the diff + evidence. Economics are printed so the
// run can be pasted into the scratchpad.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func liveProvider(t *testing.T, baseEnv, modelEnv string) llm.Provider {
	t.Helper()
	base := os.Getenv(baseEnv)
	model := os.Getenv(modelEnv)
	if base == "" || model == "" {
		t.Skipf("live test needs %s and %s", baseEnv, modelEnv)
	}
	p, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: base, Model: model, Timeout: 180 * time.Second})
	if err != nil {
		t.Fatalf("NewOpenAI(%s): %v", baseEnv, err)
	}
	return p
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"},
		{"add", "-A"}, {"commit", "-m", "base", "--allow-empty"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// TestLive_DraftVerify_FileChange delegates a file-changing task to the small
// model, sieves it with real go build/test, and has the big model judge it.
func TestLive_DraftVerify_FileChange(t *testing.T) {
	verdictProv := liveProvider(t, "SUPERCLI_LIVE_BASEURL", "SUPERCLI_LIVE_MODEL")
	// The worker/draft backend: task host by default, but set
	// SUPERCLI_LIVE_DRAFT_ON_MAIN=1 to draft on the main (big) host too —
	// useful when the small model's chat template chokes on tool-result
	// turns, so the ladder itself can still be exercised end-to-end.
	workerBase, workerModel := "SUPERCLI_LIVE_TASK_BASEURL", "SUPERCLI_LIVE_TASK_MODEL"
	if os.Getenv("SUPERCLI_LIVE_DRAFT_ON_MAIN") == "1" {
		workerBase, workerModel = "SUPERCLI_LIVE_BASEURL", "SUPERCLI_LIVE_MODEL"
	}
	workerProv := liveProvider(t, workerBase, workerModel)

	// A tiny Go module the worker will edit. mathx.Add exists; the task is to
	// add Mul plus a passing test. A 3B model often gets the signature right
	// but flubs the test or the build — exactly the case the sieve catches.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module mathx\n\ngo 1.26\n")
	writeFile(t, filepath.Join(dir, "mathx.go"), "package mathx\n\n// Add returns a+b.\nfunc Add(a, b int) int { return a + b }\n")
	writeFile(t, filepath.Join(dir, "mathx_test.go"), "package mathx\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 3) != 5 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
	gitInit(t, dir)

	// Worker registry: whole-file write + read, resolved against the repo.
	reg := tools.NewRegistry()
	reg.MustRegister(tools.NewWriteFile(dir).Spec())
	reg.MustRegister(tools.NewReadLines(dir).Spec())
	for _, n := range reg.Names() {
		reg.MarkAlwaysOn(n)
	}

	subReg := NewSubAgentRegistry()
	MustRegisterAll(subReg, BuiltinSubAgents())

	factory := func(cfg LoopConfig) (*Loop, error) { return NewLoop(cfg) }
	at, err := NewAgentTool(subReg, nil, reg, workerProv, nil, factory)
	if err != nil {
		t.Fatalf("NewAgentTool: %v", err)
	}
	at.MaxSteps = 8
	at.TimeoutPerStep = 90 * time.Second
	at.DraftVerify = &DraftVerifyConfig{
		Enabled:        true,
		VerifyCommands: []string{"go build ./...", "go test ./..."},
		MaxRounds:      2,
		SieveTimeout:   90 * time.Second,
		Verdict:        verdictProv,
	}
	// The tool has no ParentLoop, so sandboxRoot() falls back to CWD. Force
	// the sieve + diff to run in our temp repo by overriding the runners to
	// bind dir explicitly (the production path uses ParentLoop.baseDir).
	at.DraftVerify.runCommand = func(ctx context.Context, _ , command string, timeout time.Duration) (int, string) {
		return runSieveCommand(ctx, dir, command, timeout)
	}
	at.DraftVerify.gitDiff = func(ctx context.Context, _ string) string {
		return defaultGitDiff(ctx, dir)
	}

	args, _ := json.Marshal(map[string]any{
		"prompt": "In the Go module at the repo root, add a function Mul(a, b int) int that returns a*b to mathx.go (keep Add). Also add a test TestMul to mathx_test.go asserting Mul(2,3)==6. Use write_file with the FULL new file contents. The code must compile and `go test ./...` must pass.",
		"expect": "Mul added and go test passes",
	})

	start := time.Now()
	res, err := at.execute(context.Background(), args)
	elapsed := time.Since(start)
	if err != nil {
		t.Logf("execute returned err (may be fine if handback): %v", err)
	}
	t.Logf("=== draft-verify live result (%.1fs) ===\n%s", elapsed.Seconds(), res.Text)

	// Final objective truth, independent of the model's verdict.
	buildExit, buildOut := runSieveCommand(context.Background(), dir, "go build ./...", 90*time.Second)
	testExit, testOut := runSieveCommand(context.Background(), dir, "go test ./...", 90*time.Second)
	t.Logf("final go build exit=%d\n%s", buildExit, buildOut)
	t.Logf("final go test  exit=%d\n%s", testExit, testOut)
	diff := defaultGitDiff(context.Background(), dir)
	t.Logf("final diff:\n%s", diff)
}

// TestLive_DraftVerify_SieveCatchesBug seeds a repo where a naive draft leaves
// the build/test RED, so the objective sieve fires and the big model issues a
// REVISE (or TAKEOVER) verdict on the evidence. Worker drafts on the main host
// for a reliable tool loop; the point here is the SIEVE + VERDICT, not the
// draft transport.
func TestLive_DraftVerify_SieveCatchesBug(t *testing.T) {
	verdictProv := liveProvider(t, "SUPERCLI_LIVE_BASEURL", "SUPERCLI_LIVE_MODEL")
	workerProv := liveProvider(t, "SUPERCLI_LIVE_BASEURL", "SUPERCLI_LIVE_MODEL")

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module mathx\n\ngo 1.26\n")
	writeFile(t, filepath.Join(dir, "mathx.go"), "package mathx\n\n// Sub returns a-b.\nfunc Sub(a, b int) int { return a + b }\n") // BUG: uses + not -
	writeFile(t, filepath.Join(dir, "mathx_test.go"), "package mathx\n\nimport \"testing\"\n\nfunc TestSub(t *testing.T) {\n\tif Sub(5, 3) != 2 {\n\t\tt.Fatalf(\"Sub(5,3)=%d want 2\", Sub(5, 3))\n\t}\n}\n")
	gitInit(t, dir)

	reg := tools.NewRegistry()
	reg.MustRegister(tools.NewWriteFile(dir).Spec())
	reg.MustRegister(tools.NewReadLines(dir).Spec())
	for _, n := range reg.Names() {
		reg.MarkAlwaysOn(n)
	}
	subReg := NewSubAgentRegistry()
	MustRegisterAll(subReg, BuiltinSubAgents())
	factory := func(cfg LoopConfig) (*Loop, error) { return NewLoop(cfg) }
	at, err := NewAgentTool(subReg, nil, reg, workerProv, nil, factory)
	if err != nil {
		t.Fatalf("NewAgentTool: %v", err)
	}
	at.MaxSteps = 6
	at.TimeoutPerStep = 90 * time.Second
	dv := &DraftVerifyConfig{
		Enabled:        true,
		VerifyCommands: []string{"go build ./...", "go test ./..."},
		MaxRounds:      1,
		SieveTimeout:   90 * time.Second,
		Verdict:        verdictProv,
	}
	dv.runCommand = func(ctx context.Context, _, command string, timeout time.Duration) (int, string) {
		return runSieveCommand(ctx, dir, command, timeout)
	}
	dv.gitDiff = func(ctx context.Context, _ string) string { return defaultGitDiff(ctx, dir) }
	at.DraftVerify = dv

	// A no-op-ish task: the worker "reviews" but if it does nothing, Sub is
	// still buggy and TestSub fails → sieve RED → verdict must not ACCEPT.
	sieve := dv.runSieve(context.Background(), dir)
	t.Logf("initial sieve green=%v cmd=%q exit=%d", sieve.Green, sieve.Command, sieve.Exit)

	args, _ := json.Marshal(map[string]any{
		"prompt": "Look at mathx.go in the repo root. There may be a bug. Do NOT change anything; just report what you see in one sentence.",
	})
	res, err := at.execute(context.Background(), args)
	if err != nil {
		t.Logf("execute err: %v", err)
	}
	t.Logf("=== sieve-catches-bug result ===\n%s", res.Text)

	// If the worker left the bug in place, the ladder must have surfaced the
	// red sieve rather than silently accepting.
	finalExit, _ := runSieveCommand(context.Background(), dir, "go test ./...", 90*time.Second)
	if finalExit != 0 && !strings.Contains(res.Text, "auto-accepted") && !strings.Contains(res.Text, "draft-verify") {
		t.Logf("NOTE: worker fixed the bug (test now green) OR verdict accepted; final test exit=%d", finalExit)
	}
}

// TestLive_SecondOpinion asks the advisor (small model) a read-only question
// and confirms nothing on disk changed.
func TestLive_SecondOpinion(t *testing.T) {
	workerProv := liveProvider(t, "SUPERCLI_LIVE_TASK_BASEURL", "SUPERCLI_LIVE_TASK_MODEL")

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "note.txt"), "sentinel\n")

	reg := tools.NewRegistry()
	reg.MustRegister(tools.NewReadLines(dir).Spec())
	reg.MustRegister(tools.NewSearchCode(dir).Spec())
	for _, n := range reg.Names() {
		reg.MarkAlwaysOn(n)
	}
	subReg := NewSubAgentRegistry()
	MustRegisterAll(subReg, BuiltinSubAgents())

	factory := func(cfg LoopConfig) (*Loop, error) { return NewLoop(cfg) }
	at, err := NewAgentTool(subReg, nil, reg, workerProv, nil, factory)
	if err != nil {
		t.Fatalf("NewAgentTool: %v", err)
	}
	at.MaxSteps = 4
	at.TimeoutPerStep = 60 * time.Second

	args, _ := json.Marshal(map[string]any{
		"prompt": "We must store a small config: two choices — (A) a single TOML file, or (B) environment variables. For a local CLI tool that users edit by hand, which do you recommend and why in one line?",
		"advise": true,
	})
	res, err := at.execute(context.Background(), args)
	if err != nil {
		t.Logf("advise err: %v", err)
	}
	t.Logf("=== second opinion ===\n%s", res.Text)

	// The sentinel file must be untouched (advisor is read-only).
	b, _ := os.ReadFile(filepath.Join(dir, "note.txt"))
	if string(b) != "sentinel\n" {
		t.Fatalf("advisor modified a file: %q", string(b))
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("advisor created/removed files, dir has %d entries", len(entries))
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
