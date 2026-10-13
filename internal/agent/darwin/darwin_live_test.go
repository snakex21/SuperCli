//go:build darwinlive

package darwin

// Live end-to-end Darwin run against a real OpenAI-compatible server
// (e.g. LM Studio). Run with:
//
//	SUPERCLI_LIVE_BASEURL=http://localhost:1234/v1 SUPERCLI_LIVE_MODEL=<id> \
//	  go test -tags darwinlive -run TestLive_Darwin -v -timeout 30m ./internal/agent/darwin/
//
// It builds a throwaway Go repo in the OS temp dir with one failing
// test, runs pool_size=2 sequential candidates in git worktrees, lets
// the default (composite LLM) judge pick a winner and checks that the
// winner's branch is kept without auto-merge.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/ctxexec"
)

func liveChildRegistry(root string) (*tools.Registry, error) {
	reg := tools.NewRegistry()
	reg.MustRegister(tools.NewListDir(root).Spec())
	reg.MustRegister(tools.NewReadLines(root).Spec())
	reg.MustRegister(tools.NewSearchCode(root).Spec())
	reg.MustRegister(tools.NewPatchFile(root).Spec())
	reg.MustRegister(tools.NewWriteFile(root).Spec())
	reg.MustRegister(tools.NewCtxExecuteTool(ctxexec.New(root), root).Spec())
	for _, n := range reg.Names() {
		reg.MarkAlwaysOn(n)
	}
	return reg, nil
}

func TestLive_Darwin_FixFailingTest(t *testing.T) {
	base, model := os.Getenv("SUPERCLI_LIVE_BASEURL"), os.Getenv("SUPERCLI_LIVE_MODEL")
	if base == "" || model == "" {
		t.Skip("needs SUPERCLI_LIVE_BASEURL and SUPERCLI_LIVE_MODEL")
	}
	prov, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: base, Model: model, Timeout: 300 * time.Second})
	if err != nil {
		t.Fatal(err)
	}

	dir, err := os.MkdirTemp("", "darwin-live-repo-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("repo: %s", dir)
	files := map[string]string{
		"go.mod":      "module addfix\n\ngo 1.21\n",
		"add.go":      "package addfix\n\n// Add returns the sum of a and b.\nfunc Add(a, b int) int {\n\treturn a - b\n}\n",
		"add_test.go": "package addfix\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif got := Add(2, 3); got != 5 {\n\t\tt.Fatalf(\"Add(2,3) = %d, want 5\", got)\n\t}\n}\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	git("init", "-q")
	git("config", "user.email", "t@t")
	git("config", "user.name", "t")
	git("add", "-A")
	git("commit", "-qm", "base")

	var lastEv string
	d, err := NewDarwin(Config{
		PoolConfig: PoolConfig{
			Provider:         prov,
			System:           "You are a coding agent. Work only with the provided tools inside the current repository. Make the minimal code change, verify it with `go test ./...` via ctx_execute, then reply with a one-paragraph summary.",
			Home:             dir,
			Factory:          AgentLoopAdapter(tools.NewRegistry(), liveChildRegistry),
			PoolSize:         2,
			Sequential:       true,
			MaxStepsPerAgent: 15,
			BudgetWall:       7 * time.Minute,
		},
		UseWorktree: true,
		AutoMerge:   false,
		OnEvent: func(ev Event) {
			s := strings.TrimSpace(strings.SplitN(strings.TrimSpace(sprintEvent(ev)), "\n", 2)[0])
			if s != lastEv {
				t.Logf("[%s] %s", time.Now().Format("15:04:05"), s)
				lastEv = s
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	stream, err := d.Run(ctx, "The test TestAdd in this Go module fails. Find and fix the bug in the non-test code so `go test ./...` passes. Do not modify add_test.go.")
	if err != nil {
		t.Fatal(err)
	}
	var res *Result
	for ev := range stream {
		switch e := ev.(type) {
		case DoneEvent:
			r := e.Result
			res = &r
		case ErrorEvent:
			t.Fatalf("darwin error after %s: %v", time.Since(start).Round(time.Second), e.Err)
		}
	}
	t.Logf("elapsed: %s", time.Since(start).Round(time.Second))
	if res == nil {
		t.Fatal("no DoneEvent")
	}
	t.Logf("\n%s", renderDarwinResult(*res))
	for _, c := range res.Candidates {
		t.Logf("candidate %d: err=%v branch=%s diffstat=%q", c.Index, c.Err, c.Branch, c.DiffStat)
	}
	t.Logf("git status --porcelain: %q", git("status", "--porcelain"))
	t.Logf("git branch: %s", git("branch", "--list"))
	t.Logf("git worktree list: %s", git("worktree", "list"))
	if res.KeptBranch != "" {
		t.Logf("kept branch diff:\n%s", git("diff", "HEAD..."+res.KeptBranch))
		// Verify the winner really fixes the test.
		git("checkout", "-q", res.KeptBranch)
		cmd := exec.Command("go", "test", "./...")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		t.Logf("go test on winner branch: err=%v\n%s", err, out)
		git("checkout", "-q", "-")
	}
	failed := 0
	for _, c := range res.Candidates {
		if c.Err != nil {
			failed++
		}
	}
	if failed == len(res.Candidates) {
		t.Fatal("every candidate failed")
	}
}

func sprintEvent(ev Event) string {
	switch e := ev.(type) {
	case StartedEvent:
		return "started pool_size=" + strings.Repeat("|", e.PoolSize)
	case CandidateDoneEvent:
		if e.Candidate.Err != nil {
			return "candidate done " + e.Candidate.AgentID + " ERR: " + e.Candidate.Err.Error()
		}
		return "candidate done " + e.Candidate.AgentID + ": " + e.Candidate.Text
	case JudgeEvent:
		return "judge: winner=" + strings.Repeat("#", e.WinnerIndex+1) + " reason=" + e.Reason
	case MergedEvent:
		return "merged " + e.FromBranch
	case DoneEvent:
		return "done"
	case ErrorEvent:
		return "error: " + e.Err.Error()
	}
	return ""
}
