package darwin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/tools"
)

// --- WorktreeManager.CommitAndDiff ---

func TestWorktreeManager_CommitAndDiff_CapturesChanges(t *testing.T) {
	requireGit(t)
	dir := makeRepo(t)
	m := NewWorktreeManager(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	branch, path, err := m.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer m.CleanupAll(context.Background())
	if err := os.WriteFile(filepath.Join(path, "new.txt"), []byte("change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	diff, err := m.CommitAndDiff(ctx, branch)
	if err != nil {
		t.Fatalf("CommitAndDiff: %v", err)
	}
	if !strings.Contains(diff, "new.txt") {
		t.Errorf("diff should mention new.txt, got: %q", diff)
	}
	// The commit must make the branch mergeable into the base.
	if err := m.Merge(ctx, branch, "HEAD"); err != nil {
		t.Fatalf("merge after CommitAndDiff: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); err != nil {
		t.Errorf("merged file missing in base repo: %v", err)
	}
}

func TestWorktreeManager_CommitAndDiff_EmptyWhenNoChanges(t *testing.T) {
	requireGit(t)
	dir := makeRepo(t)
	m := NewWorktreeManager(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	branch, _, err := m.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer m.CleanupAll(context.Background())
	diff, err := m.CommitAndDiff(ctx, branch)
	if err != nil {
		t.Fatalf("CommitAndDiff: %v", err)
	}
	if diff != "" {
		t.Errorf("expected empty diff for untouched worktree, got %q", diff)
	}
}

func TestWorktreeManager_CommitAndDiff_UnknownBranch(t *testing.T) {
	requireGit(t)
	m := NewWorktreeManager(makeRepo(t))
	if _, err := m.CommitAndDiff(context.Background(), "nope"); err == nil {
		t.Fatal("expected error for unknown branch")
	}
}

// --- SpawnPool per-agent homes + agent stamping ---

func TestSpawnPool_PerAgentHomesReachFactory(t *testing.T) {
	var mu sync.Mutex
	var homes []string
	homesIn := []string{"/wt/a", "/wt/b", "/wt/c"}
	stream, err := SpawnPool(context.Background(), PoolConfig{
		Provider: providerStub{},
		System:   "sys",
		Home:     "/fallback",
		Homes:    homesIn,
		PoolSize: 3,
		Factory: func(cfg LoopConfig) (Loop, error) {
			mu.Lock()
			homes = append(homes, cfg.Home)
			mu.Unlock()
			return &stubLoop{script: []LoopEvent{LoopDoneEvent{Text: "ok"}}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	agents := map[int]bool{}
	for ev := range stream {
		if de, ok := ev.(LoopDoneEvent); ok {
			agents[de.Agent] = true
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(homes) != 3 {
		t.Fatalf("factory called %d times, want 3", len(homes))
	}
	got := map[string]bool{}
	for _, h := range homes {
		got[h] = true
	}
	for _, h := range homesIn {
		if !got[h] {
			t.Errorf("home %q never handed to a factory; got %v", h, homes)
		}
	}
	for i := 0; i < 3; i++ {
		if !agents[i] {
			t.Errorf("no LoopDoneEvent stamped with Agent=%d", i)
		}
	}
}

func TestSpawnPool_HomesShorterThanPoolFallsBack(t *testing.T) {
	var mu sync.Mutex
	var homes []string
	stream, err := SpawnPool(context.Background(), PoolConfig{
		Provider: providerStub{},
		System:   "sys",
		Home:     "/fallback",
		Homes:    []string{"/wt/a"},
		PoolSize: 2,
		Factory: func(cfg LoopConfig) (Loop, error) {
			mu.Lock()
			homes = append(homes, cfg.Home)
			mu.Unlock()
			return &stubLoop{script: []LoopEvent{LoopDoneEvent{Text: "ok"}}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range stream {
	}
	mu.Lock()
	defer mu.Unlock()
	got := map[string]bool{}
	for _, h := range homes {
		got[h] = true
	}
	if !got["/wt/a"] || !got["/fallback"] {
		t.Errorf("want one agent in /wt/a and one in /fallback, got %v", homes)
	}
}

// --- AgentLoopAdapter registry rooting ---

func TestAgentLoopAdapter_BuildsPerWorktreeRegistry(t *testing.T) {
	var mu sync.Mutex
	var roots []string
	fact := AgentLoopAdapter(tools.NewRegistry(), func(root string) (*tools.Registry, error) {
		mu.Lock()
		roots = append(roots, root)
		mu.Unlock()
		return tools.NewRegistry(), nil
	})
	if _, err := fact(LoopConfig{Provider: providerStub{}, System: "sys", Home: "/wt/x"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(roots) != 1 || roots[0] != "/wt/x" {
		t.Fatalf("builder roots = %v, want [/wt/x]", roots)
	}
}

func TestAgentLoopAdapter_NilBuilderUsesSharedRegistry(t *testing.T) {
	fact := AgentLoopAdapter(tools.NewRegistry(), nil)
	if _, err := fact(LoopConfig{Provider: providerStub{}, System: "sys", Home: "/wt/x"}); err != nil {
		t.Fatalf("factory with nil builder should still work: %v", err)
	}
}

// --- End-to-end: worktree isolation, diff to judge, merge ---

func TestRun_WorktreeIsolation_DiffAndMerge(t *testing.T) {
	requireGit(t)
	dir := makeRepo(t)
	// Each agent writes a file into its own Home (its
	// worktree) — exactly what a worktree-rooted tool
	// registry lets real children do.
	factory := func(cfg LoopConfig) (Loop, error) {
		if err := os.WriteFile(filepath.Join(cfg.Home, "agent.txt"), []byte("work\n"), 0o644); err != nil {
			return nil, err
		}
		return &stubLoop{script: []LoopEvent{LoopDoneEvent{Text: "did the work"}}}, nil
	}
	var judgedDiffs int
	judge := judgeFunc(func(_ context.Context, _ string, cands []Candidate) (Verdict, error) {
		for _, c := range cands {
			if c.Diff != "" {
				judgedDiffs++
			}
		}
		return Verdict{WinnerIndex: 0, Score: 0.9, Reason: "test"}, nil
	})
	d, err := NewDarwin(Config{
		PoolConfig: PoolConfig{
			Provider: providerStub{},
			System:   "sys",
			Home:     dir,
			Factory:  factory,
			PoolSize: 2,
		},
		Judge:       judge,
		UseWorktree: true,
		AutoMerge:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	stream, err := d.Run(ctx, "do the work")
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
		t.Fatal("no DoneEvent")
	}
	if judgedDiffs == 0 {
		t.Error("judge never saw a candidate diff")
	}
	if res.Winner == nil {
		t.Fatal("no winner")
	}
	if res.Winner.Diff == "" || !strings.Contains(res.Winner.Diff, "agent.txt") {
		t.Errorf("winner diff should contain agent.txt, got %q", res.Winner.Diff)
	}
	if res.Winner.WorktreePath == "" {
		t.Error("winner should carry its worktree path")
	}
	if !res.Merged {
		t.Fatal("expected auto-merge of the winner")
	}
	if _, err := os.Stat(filepath.Join(dir, "agent.txt")); err != nil {
		t.Errorf("winner's file should exist in base repo after merge: %v", err)
	}
	if res.Note != "" {
		t.Errorf("unexpected note: %q", res.Note)
	}
}

// judgeFunc adapts a func to the Judge interface.
type judgeFunc func(ctx context.Context, prompt string, cands []Candidate) (Verdict, error)

func (f judgeFunc) Judge(ctx context.Context, prompt string, cands []Candidate) (Verdict, error) {
	return f(ctx, prompt, cands)
}

// --- Graceful degradation in non-git dirs ---

func TestRun_NonGitDir_DegradesWithNote(t *testing.T) {
	requireGit(t)
	dir := t.TempDir() // not a git repo
	d, err := NewDarwin(Config{
		PoolConfig: PoolConfig{
			Provider: providerStub{},
			System:   "sys",
			Home:     dir,
			Factory: func(LoopConfig) (Loop, error) {
				return &stubLoop{script: []LoopEvent{LoopDoneEvent{Text: "answer"}}}, nil
			},
			PoolSize: 2,
		},
		Judge:       &deterministicJudge{},
		UseWorktree: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := d.Run(context.Background(), "task")
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
		t.Fatal("no DoneEvent")
	}
	if res.Note == "" || !strings.Contains(res.Note, "not a git repository") {
		t.Errorf("expected degradation note, got %q", res.Note)
	}
	for _, c := range res.Candidates {
		if c.Branch != "" || c.Diff != "" || c.WorktreePath != "" {
			t.Errorf("non-git run should have no branch/diff/worktree, got %+v", c)
		}
	}
	if res.Winner == nil || res.Winner.Text != "answer" {
		t.Errorf("text-only mode should still pick a winner, got %+v", res.Winner)
	}
}

// --- Judge prompt includes diffs ---

func TestRenderJudgePrompt_IncludesDiff(t *testing.T) {
	p := renderJudgePrompt("task", []Candidate{
		{Text: "answer", Diff: "diff --git a/x b/x\n+added line"},
	})
	if !strings.Contains(p, "Diff of changes made by this candidate") {
		t.Error("judge prompt missing diff section")
	}
	if !strings.Contains(p, "+added line") {
		t.Error("judge prompt missing diff body")
	}
}

func TestRenderJudgePrompt_TruncatesLongDiff(t *testing.T) {
	long := strings.Repeat("x", 9000)
	p := renderJudgePrompt("task", []Candidate{{Text: "a", Diff: long}})
	if !strings.Contains(p, "[diff truncated]") {
		t.Error("long diff should be truncated")
	}
	if strings.Contains(p, strings.Repeat("x", 8001)) {
		t.Error("diff should be cut at 8000 chars")
	}
}

func TestHeuristicJudge_DiffRewarded(t *testing.T) {
	j := NewHeuristicJudge()
	v, err := j.Judge(context.Background(), "task", []Candidate{
		{Index: 0, Text: "a plausible long enough answer with detail here"},
		{Index: 1, Text: "a plausible long enough answer with detail here", Diff: "diff --git ..."},
	})
	if err != nil {
		t.Fatal(err)
	}
	if v.WinnerIndex != 1 {
		t.Errorf("candidate with a diff should win, got index %d", v.WinnerIndex)
	}
}
