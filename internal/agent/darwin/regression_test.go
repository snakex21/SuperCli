package darwin

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// promptRecordingProvider answers every call with a short text and
// records the last user message it was given.
type promptRecordingProvider struct {
	mu       sync.Mutex
	lastUser string
}

func (p *promptRecordingProvider) Name() string { return "recording" }

func (p *promptRecordingProvider) Complete(_ context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	p.mu.Lock()
	for _, m := range msgs {
		if m.Role == llm.RoleUser {
			p.lastUser = m.Content
		}
	}
	p.mu.Unlock()
	ch := make(chan llm.Delta, 2)
	ch <- llm.Delta{Content: "candidate answer"}
	ch <- llm.Delta{FinishReason: "stop"}
	close(ch)
	return ch, nil
}

// Regression: SpawnPool used to call loop.Run(ctx, "") and the real
// agent.Loop rejects an empty prompt, so every candidate failed.
func TestSpawnPool_RealAgentLoopReceivesPrompt(t *testing.T) {
	prov := &promptRecordingProvider{}
	stream, err := SpawnPool(context.Background(), PoolConfig{
		Provider:   prov,
		System:     "sys",
		Prompt:     "fix the failing test",
		Home:       t.TempDir(),
		PoolSize:   1,
		Sequential: true,
		Factory:    AgentLoopAdapter(tools.NewRegistry(), nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	var done *LoopDoneEvent
	for ev := range stream {
		switch e := ev.(type) {
		case LoopErrorEvent:
			t.Fatalf("candidate failed: %v", e.Err)
		case LoopDoneEvent:
			d := e
			done = &d
		}
	}
	if done == nil {
		t.Fatal("no LoopDoneEvent from real agent loop")
	}
	if !strings.Contains(done.Text, "candidate answer") {
		t.Errorf("done text = %q", done.Text)
	}
	prov.mu.Lock()
	defer prov.mu.Unlock()
	if !strings.Contains(prov.lastUser, "fix the failing test") {
		t.Errorf("provider never saw the prompt; last user msg = %q", prov.lastUser)
	}
}

func TestSpawnPool_RequiresPrompt(t *testing.T) {
	cfg := validPool(t, func(LoopConfig) (Loop, error) { return &stubLoop{}, nil })
	cfg.Prompt = "  "
	if _, err := SpawnPool(context.Background(), cfg); err == nil {
		t.Fatal("expected error for empty Prompt")
	}
}

// emptyPromptRejectingLoop mimics agent.Loop.Run's guard.
type emptyPromptRejectingLoop struct{}

func (emptyPromptRejectingLoop) Run(_ context.Context, prompt string) (<-chan LoopEvent, error) {
	if prompt == "" {
		return nil, errors.New("prompt is empty")
	}
	ch := make(chan LoopEvent, 1)
	ch <- LoopDoneEvent{Text: "echo: " + prompt}
	close(ch)
	return ch, nil
}

func TestDarwinRun_PassesPromptToCandidates(t *testing.T) {
	cfg := validConfig(t)
	cfg.PoolConfig.PoolSize = 2
	cfg.PoolConfig.Factory = func(LoopConfig) (Loop, error) { return emptyPromptRejectingLoop{}, nil }
	cfg.Judge = &deterministicJudge{idx: -1}
	d, err := NewDarwin(cfg)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := d.Run(context.Background(), "the task")
	if err != nil {
		t.Fatal(err)
	}
	var res *Result
	for ev := range stream {
		if de, ok := ev.(DoneEvent); ok {
			r := de.Result
			res = &r
		}
	}
	if res == nil {
		t.Fatal("no DoneEvent")
	}
	for _, c := range res.Candidates {
		if c.Err != nil {
			t.Errorf("candidate %d failed: %v", c.Index, c.Err)
		}
		if c.Text != "echo: the task" {
			t.Errorf("candidate %d text = %q", c.Index, c.Text)
		}
	}
}

func TestEmitOrSkip_TerminalEventsBlockUntilConsumed(t *testing.T) {
	ch := make(chan LoopEvent, 1)
	ctx := context.Background()
	emitOrSkip(ctx, ch, LoopMessageEvent{Text: "fill"})
	// Progress event on a full channel is dropped without blocking.
	emitOrSkip(ctx, ch, LoopMessageEvent{Text: "dropped"})

	sent := make(chan struct{})
	go func() {
		emitOrSkip(ctx, ch, LoopDoneEvent{Text: "final"})
		close(sent)
	}()
	select {
	case <-sent:
		t.Fatal("terminal event returned while channel was full (dropped)")
	case <-time.After(50 * time.Millisecond):
	}
	if ev := <-ch; ev.(LoopMessageEvent).Text != "fill" {
		t.Fatalf("first event = %#v", ev)
	}
	<-sent
	if ev := <-ch; ev.(LoopDoneEvent).Text != "final" {
		t.Fatalf("terminal event = %#v", ev)
	}
	select {
	case ev := <-ch:
		t.Fatalf("unexpected extra event %#v", ev)
	default:
	}
}

func TestEmitOrSkip_TerminalEventReleasedByCancel(t *testing.T) {
	ch := make(chan LoopEvent, 1)
	ch <- LoopMessageEvent{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		emitOrSkip(ctx, ch, LoopErrorEvent{Err: errors.New("x")})
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("terminal emit not released by ctx cancel")
	}
}

// A slow consumer must still get every candidate's terminal event.
func TestSpawnPool_SlowConsumerGetsAllTerminalEvents(t *testing.T) {
	script := make([]LoopEvent, 0, 50)
	for i := 0; i < 49; i++ {
		script = append(script, LoopMessageEvent{Text: "x"})
	}
	script = append(script, LoopDoneEvent{Text: "ok"})
	cfg := validPool(t, func(LoopConfig) (Loop, error) { return &stubLoop{script: script}, nil })
	cfg.PoolSize = 3
	stream, err := SpawnPool(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // let the buffer overflow
	done := 0
	for ev := range stream {
		if _, ok := ev.(LoopDoneEvent); ok {
			done++
		}
	}
	if done != 3 {
		t.Fatalf("got %d LoopDoneEvents, want 3", done)
	}
}

func TestSpawnPool_MaxStepsReachesFactory(t *testing.T) {
	for _, tc := range []struct{ in, want int }{{0, DefaultMaxStepsPerAgent}, {7, 7}} {
		var got int32
		cfg := validPool(t, func(c LoopConfig) (Loop, error) {
			atomic.StoreInt32(&got, int32(c.MaxSteps))
			return &stubLoop{script: []LoopEvent{LoopDoneEvent{}}}, nil
		})
		cfg.PoolSize = 1
		cfg.MaxStepsPerAgent = tc.in
		stream, err := SpawnPool(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		for range stream {
		}
		if int(got) != tc.want {
			t.Errorf("MaxStepsPerAgent=%d: factory saw %d, want %d", tc.in, got, tc.want)
		}
	}
}

// sleepLoop finishes after d unless ctx ends first; in that case it
// closes the channel without a terminal event (like the adapter).
type sleepLoop struct{ d time.Duration }

func (s sleepLoop) Run(ctx context.Context, _ string) (<-chan LoopEvent, error) {
	ch := make(chan LoopEvent, 1)
	go func() {
		defer close(ch)
		select {
		case <-time.After(s.d):
			ch <- LoopDoneEvent{Text: "ok"}
		case <-ctx.Done():
		}
	}()
	return ch, nil
}

// BudgetWall is per candidate: sequential agents that each fit the
// budget all finish even though together they exceed it.
func TestSpawnPool_SequentialBudgetIsPerAgent(t *testing.T) {
	cfg := validPool(t, func(LoopConfig) (Loop, error) { return sleepLoop{d: 120 * time.Millisecond}, nil })
	cfg.PoolSize = 3
	cfg.Sequential = true
	cfg.BudgetWall = 300 * time.Millisecond
	stream, err := SpawnPool(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	done := 0
	for ev := range stream {
		switch e := ev.(type) {
		case LoopDoneEvent:
			done++
		case LoopErrorEvent:
			t.Errorf("agent %d failed: %v", e.Agent, e.Err)
		}
	}
	if done != 3 {
		t.Fatalf("done = %d, want 3", done)
	}
}

func TestSpawnPool_AgentOverBudgetBecomesError(t *testing.T) {
	cfg := validPool(t, func(LoopConfig) (Loop, error) { return sleepLoop{d: time.Minute}, nil })
	cfg.PoolSize = 2
	cfg.Sequential = true
	cfg.BudgetWall = 50 * time.Millisecond
	stream, err := SpawnPool(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	errs := map[int]bool{}
	for ev := range stream {
		if e, ok := ev.(LoopErrorEvent); ok {
			errs[e.Agent] = true
			if !strings.Contains(e.Err.Error(), "wall budget") {
				t.Errorf("err = %v", e.Err)
			}
		}
	}
	if !errs[0] || !errs[1] {
		t.Fatalf("want a budget error for both agents, got %v", errs)
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

// Without auto-merge the winner's branch must survive cleanup (so the
// user can merge it); losers are removed; worktrees live outside the
// workspace and leave `git status` clean.
func TestRun_NoAutoMerge_KeepsWinnerBranch(t *testing.T) {
	requireGit(t)
	dir := makeRepo(t)
	var mu sync.Mutex
	var homes []string
	factory := func(cfg LoopConfig) (Loop, error) {
		mu.Lock()
		homes = append(homes, cfg.Home)
		mu.Unlock()
		if err := os.WriteFile(filepath.Join(cfg.Home, "fix.txt"), []byte("fixed\n"), 0o644); err != nil {
			return nil, err
		}
		return &stubLoop{script: []LoopEvent{LoopDoneEvent{Text: "done"}}}, nil
	}
	d, err := NewDarwin(Config{
		PoolConfig: PoolConfig{
			Provider:   providerStub{},
			System:     "sys",
			Home:       dir,
			Factory:    factory,
			PoolSize:   2,
			Sequential: true,
		},
		Judge: judgeFunc(func(context.Context, string, []Candidate) (Verdict, error) {
			return Verdict{WinnerIndex: 1, Score: 1, Reason: "r"}, nil
		}),
		UseWorktree: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	stream, err := d.Run(ctx, "fix it")
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
	if res == nil || res.Winner == nil {
		t.Fatal("no result/winner")
	}
	if res.Merged {
		t.Fatal("must not merge with AutoMerge=false")
	}
	if res.KeptBranch == "" || res.KeptBranch != res.Winner.Branch {
		t.Fatalf("KeptBranch = %q, winner branch = %q", res.KeptBranch, res.Winner.Branch)
	}
	if !branchExists(t, dir, res.KeptBranch) {
		t.Fatalf("winner branch %s was deleted", res.KeptBranch)
	}
	loser := res.Candidates[0].Branch
	if loser != "" && branchExists(t, dir, loser) {
		t.Errorf("loser branch %s should be deleted", loser)
	}
	if !strings.Contains(res.Winner.DiffStat, "fix.txt") {
		t.Errorf("winner diffstat = %q", res.Winner.DiffStat)
	}
	if got := gitOut(t, dir, "diff", "--name-only", "HEAD..."+res.KeptBranch); !strings.Contains(got, "fix.txt") {
		t.Errorf("kept branch diff = %q", got)
	}
	mu.Lock()
	for _, h := range homes {
		if rel, err := filepath.Rel(dir, h); err == nil && !strings.HasPrefix(rel, "..") {
			t.Errorf("worktree %s created inside workspace %s", h, dir)
		}
		if _, err := os.Stat(h); !os.IsNotExist(err) {
			t.Errorf("worktree dir %s not removed (err=%v)", h, err)
		}
	}
	mu.Unlock()
	if st := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); st != "" {
		t.Errorf("workspace git status dirty after run:\n%s", st)
	}
	if wt := gitOut(t, dir, "worktree", "list", "--porcelain"); strings.Count(wt, "worktree ") != 1 {
		t.Errorf("stale worktrees registered:\n%s", wt)
	}
	text := renderDarwinResult(*res)
	for _, want := range []string{res.KeptBranch, "git merge", "diffstat"} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered result missing %q:\n%s", want, text)
		}
	}
	_ = gitOut(t, dir, "branch", "-D", res.KeptBranch)
}
