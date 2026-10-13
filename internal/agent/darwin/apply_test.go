package darwin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// runKeepAll runs a Darwin with KeepAll where candidate n writes files[n]
// into its own worktree. The judge always picks index 0.
func runKeepAll(t *testing.T, dir string, files []string) Result {
	t.Helper()
	var next int32
	factory := func(cfg LoopConfig) (Loop, error) {
		name := files[int(atomic.AddInt32(&next, 1)-1)%len(files)]
		if err := os.WriteFile(filepath.Join(cfg.Home, name), []byte(name+"\n"), 0o644); err != nil {
			return nil, err
		}
		return &stubLoop{script: []LoopEvent{LoopDoneEvent{Text: "wrote " + name}}}, nil
	}
	d, err := NewDarwin(Config{
		PoolConfig: PoolConfig{
			Provider: providerStub{}, System: "sys", Home: dir, Factory: factory,
			PoolSize: len(files), Sequential: true,
		},
		Judge: judgeFunc(func(context.Context, string, []Candidate) (Verdict, error) {
			return Verdict{WinnerIndex: 0, Score: 0.9, Reason: "first"}, nil
		}),
		UseWorktree: true,
		KeepAll:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	stream, err := d.Run(ctx, "do it")
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
			t.Fatalf("run failed: %v", e.Err)
		}
	}
	if res == nil {
		t.Fatal("no result")
	}
	return *res
}

func TestKeepAll_ApplyLoserThenDiscard(t *testing.T) {
	requireGit(t)
	dir := makeRepo(t)
	res := runKeepAll(t, dir, []string{"one.txt", "two.txt"})
	if len(res.Candidates) != 2 {
		t.Fatalf("candidates = %d", len(res.Candidates))
	}
	var branches []string
	for _, c := range res.Candidates {
		if !c.Kept || !IsCandidateBranch(c.Branch) || !branchExists(t, dir, c.Branch) {
			t.Fatalf("candidate %d not kept: kept=%v branch=%q", c.Index, c.Kept, c.Branch)
		}
		branches = append(branches, c.Branch)
	}
	// Apply the candidate the judge did NOT pick.
	loser := res.Candidates[1-res.WinnerIndex]
	ctx := context.Background()
	if err := ApplyBranch(ctx, dir, loser.Branch); err != nil {
		t.Fatalf("apply: %v", err)
	}
	wantFile := strings.TrimPrefix(strings.TrimSpace(loser.Text), "wrote ")
	if _, err := os.Stat(filepath.Join(dir, wantFile)); err != nil {
		t.Fatalf("applied file %s missing: %v", wantFile, err)
	}
	if st := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); st != "" {
		t.Errorf("status dirty after apply:\n%s", st)
	}
	if err := DeleteBranches(ctx, dir, append(branches, "main", "not-darwin")); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for _, b := range branches {
		if branchExists(t, dir, b) {
			t.Errorf("branch %s still exists", b)
		}
	}
	if err := ApplyBranch(ctx, dir, branches[0]); err == nil {
		t.Error("apply of a deleted branch must fail")
	}
}

func TestApplyBranch_RejectsNonDarwinBranch(t *testing.T) {
	requireGit(t)
	dir := makeRepo(t)
	for _, b := range []string{"main", "master", "feature/x", "darwin-1-zz", "--force"} {
		if err := ApplyBranch(context.Background(), dir, b); err == nil {
			t.Errorf("ApplyBranch(%q) must refuse", b)
		}
	}
}

func TestApplyBranch_ConflictAbortsCleanly(t *testing.T) {
	requireGit(t)
	dir := makeRepo(t)
	gitOut(t, dir, "checkout", "-q", "-b", "darwin-1-ab")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("theirs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, dir, "commit", "-qam", "theirs")
	gitOut(t, dir, "checkout", "-q", "-")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("ours\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, dir, "commit", "-qam", "ours")
	if err := ApplyBranch(context.Background(), dir, "darwin-1-ab"); err == nil {
		t.Fatal("conflicting apply must fail")
	}
	if st := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); st != "" {
		t.Errorf("conflict left the tree dirty:\n%s", st)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if strings.TrimSpace(string(data)) != "ours" {
		t.Errorf("a.txt = %q", data)
	}
}
