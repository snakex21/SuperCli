package preflight

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/system/childproc"
)

func TestBranchStatusRemovesIndependentBranchProcess(t *testing.T) {
	var branchCalls, logCalls, statusCalls atomic.Int32
	statusEntered, logEntered := make(chan struct{}), make(chan struct{})
	block := Build(t.TempDir(), Options{LookPath: hasGit, RunGit: func(_ string, args ...string) (string, error) {
		switch args[0] {
		case "status":
			statusCalls.Add(1)
			close(statusEntered)
			select {
			case <-logEntered:
			case <-time.After(time.Second):
				return "", fmt.Errorf("log did not overlap status")
			}
			return "## feature/żółć...origin/feature/żółć [ahead 1]\n M first.go\n?? second.go", nil
		case "log":
			logCalls.Add(1)
			close(logEntered)
			select {
			case <-statusEntered:
			case <-time.After(time.Second):
				return "", fmt.Errorf("status did not overlap log")
			}
			return "abc123 newest\ndef456 older", nil
		case "rev-parse":
			branchCalls.Add(1)
			return "unexpected independent branch process", nil
		}
		return "", fmt.Errorf("unexpected Git command %v", args)
	}})
	if branchCalls.Load() != 0 || logCalls.Load() != 1 || statusCalls.Load() != 1 {
		t.Fatalf("Git calls branch/log/status = %d/%d/%d", branchCalls.Load(), logCalls.Load(), statusCalls.Load())
	}
	for _, want := range []string{"branch: feature/żółć", "HEAD: abc123 newest", " M first.go", "?? second.go", "def456 older"} {
		if !strings.Contains(block, want) {
			t.Fatalf("briefing lost %q: %s", want, block)
		}
	}
	if strings.Contains(block, "##") || strings.Contains(block, "origin/") {
		t.Fatalf("status metadata leaked into file listing: %s", block)
	}
}

func TestStatusBranchPreservesPorcelainPayload(t *testing.T) {
	for _, tc := range []struct {
		name, input, branch, worktree string
		ok                            bool
	}{
		{"clean", "## main", "main", "", true},
		{"tracking", "## feature/żółć...origin/feature/żółć [ahead 2, behind 1]\n M first.go", "feature/żółć", " M first.go", true},
		{"gone upstream", "## main...origin/main [gone]\n?? file.go", "main", "?? file.go", true},
		{"detached", "## HEAD (no branch)\nR  \"old name\" -> \"new name\"", "HEAD", "R  \"old name\" -> \"new name\"", true},
		{"CRLF", "## main\r\n M file.go\r\n?? next.go", "main", " M file.go\r\n?? next.go", true},
		{"unborn", "## No commits yet on main\n?? file.go", "", "?? file.go", true},
		{"legacy unborn", "## Initial commit on main", "", "", true},
		{"legacy runner", " M file.go\n?? next.go", "", " M file.go\n?? next.go", false},
		{"malformed", "## \n M file.go", "", "## \n M file.go", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			branch, worktree, ok := statusBranch(tc.input)
			if branch != tc.branch || worktree != tc.worktree || ok != tc.ok {
				t.Fatalf("status split = %q/%q/%v", branch, worktree, ok)
			}
		})
	}
}

func TestStatusNotRepositoryOnlySkipsExplicitNativeFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"native outside", &exec.ExitError{Stderr: []byte("fatal: not a git repository (or any of the parent directories): .git\n")}, true},
		{"explicit git dir", &exec.ExitError{Stderr: []byte("fatal: not a git repository: '/missing'\n")}, true},
		{"bare", &exec.ExitError{Stderr: []byte("fatal: this operation must be run in a work tree")}, false},
		{"permission", &exec.ExitError{Stderr: []byte("fatal: unable to read current working directory: Permission denied")}, false},
		{"translated", &exec.ExitError{Stderr: []byte("fatal: nie jest repozytorium git")}, false},
		{"injected", fmt.Errorf("fatal: not a git repository"), false},
		{"timeout", context.DeadlineExceeded, false},
		{"nil", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := gitStatusNotRepository(tc.err); got != tc.want {
				t.Fatalf("native status classification = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStatusFailureRetainsIndependentRepositoryIdentity(t *testing.T) {
	for _, statusError := range []error{context.DeadlineExceeded, fmt.Errorf("permission failure"), &exec.ExitError{Stderr: []byte("fatal: this operation must be run in a work tree")}} {
		var identityCalls atomic.Int32
		block := Build(t.TempDir(), Options{LookPath: hasGit, RunGit: func(_ string, args ...string) (string, error) {
			switch args[0] {
			case "status":
				return "", statusError
			case "log":
				return "", fmt.Errorf("log temporarily unavailable")
			case "rev-parse":
				identityCalls.Add(1)
				return "independent-branch", nil
			}
			return "", fmt.Errorf("unexpected command")
		}})
		if identityCalls.Load() != 1 || !strings.Contains(block, "branch: independent-branch") || strings.Contains(block, "working tree clean") {
			t.Fatalf("fallback identity was lost: calls=%d briefing=%q", identityCalls.Load(), block)
		}
	}
}

func TestNativeNonRepositoryFailureDoesNotSpawnThirdGitProcess(t *testing.T) {
	root := t.TempDir()
	writePF(t, filepath.Join(root, "source.go"), "local source")
	var statusCalls, logCalls, identityCalls atomic.Int32
	block := Build(root, Options{LookPath: hasGit, RunGit: func(_ string, args ...string) (string, error) {
		switch args[0] {
		case "status":
			statusCalls.Add(1)
			return "", &exec.ExitError{Stderr: []byte("fatal: not a git repository (or any of the parent directories): .git\n")}
		case "log":
			logCalls.Add(1)
			return "", fmt.Errorf("no repository log")
		case "rev-parse":
			identityCalls.Add(1)
			return "", fmt.Errorf("redundant repository lookup")
		}
		return "", fmt.Errorf("unexpected command")
	}})
	if statusCalls.Load() != 1 || logCalls.Load() != 1 || identityCalls.Load() != 0 || !strings.Contains(block, "source.go") || strings.Contains(block, "branch:") {
		t.Fatalf("non-repository fallback changed: Git calls status/log/identity=%d/%d/%d briefing=%q", statusCalls.Load(), logCalls.Load(), identityCalls.Load(), block)
	}
}

func TestNativeBranchStatusKeepsOriginalBriefingAndFreshChanges(t *testing.T) {
	root := preflightNativeFixture(t, 6)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	branch, err := realRunGit(ctx, root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	log, err := realRunGit(ctx, root, "log", "--oneline", "-8")
	if err != nil {
		t.Fatal(err)
	}
	status, err := realRunGit(ctx, root, "status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	want := Build(root, Options{LookPath: hasGit, RunGit: gitStub(branch, "", status, log)})
	if got := Build(root, Options{}); got != want {
		t.Fatalf("native branch collection changed briefing:\ngot: %q\nwant: %q", got, want)
	}
	// Log may be cached, but an immediate worktree edit must still appear.
	if err := os.WriteFile(filepath.Join(root, "tracked.go"), []byte("changed now\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Build(root, Options{}); !strings.Contains(got, " M tracked.go") {
		t.Fatalf("warm-cache preflight hid fresh worktree change: %s", got)
	}
}

func preflightNativeFixture(tb testing.TB, changed int) string {
	tb.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		tb.Skip("Git is optional; no executable available")
	}
	parent, err := filepath.Abs(filepath.Join("..", "..", "..", ".tmp", "preflight-git"))
	if err != nil {
		tb.Fatal(err)
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		tb.Fatal(err)
	}
	root, err := os.MkdirTemp(parent, "case-")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = os.RemoveAll(root) })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	run := func(args ...string) {
		tb.Helper()
		prefix := []string{"-C", root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=" + filepath.Join(root, "absent-hooks")}
		cmd := exec.CommandContext(ctx, git, append(prefix, args...)...)
		childproc.HideWindow(cmd)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(root, "absent-global-config"))
		if out, err := cmd.CombinedOutput(); err != nil {
			tb.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "fixture-branch")
	if err := os.WriteFile(filepath.Join(root, "tracked.go"), []byte("before\n"), 0o644); err != nil {
		tb.Fatal(err)
	}
	run("add", "tracked.go")
	run("commit", "-m", "fixture newest")
	for i := 0; i < changed; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("change-%03d.go", i)), []byte("new source\n"), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	return root
}

func BenchmarkPreflightNativeCold(b *testing.B) {
	for _, changed := range []int{0, 6, 200} {
		b.Run(fmt.Sprintf("status_%d", changed), func(b *testing.B) {
			root := preflightNativeFixture(b, changed)
			var calls atomic.Int64
			options := Options{RunGit: func(root string, args ...string) (string, error) {
				calls.Add(1)
				ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
				defer cancel()
				return realRunGit(ctx, root, args...)
			}}
			block := Build(root, options)
			if !strings.Contains(block, "branch: fixture-branch") || !strings.Contains(block, "fixture newest") || EstimateTokens(block) > DefaultBudget {
				b.Fatalf("invalid native briefing: %s", block)
			}
			calls.Store(0)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				preflightBriefingSink = Build(root, options)
				if preflightBriefingSink != block {
					b.Fatal("immutable native fixture produced a changed briefing")
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(calls.Load())/float64(b.N), "git-calls/op")
			b.ReportMetric(float64(EstimateTokens(block)), "estimated-tokens")
		})
	}
}

// A real non-repository directory inside the portable fixture tree. The Git
// ceiling prevents discovering this checkout's ancestor .git without any fake
// subprocess results. This records the identity-fallback tradeoff separately.
func BenchmarkPreflightNativeNonRepo(b *testing.B) {
	parent, err := filepath.Abs(filepath.Join("..", "..", "..", ".tmp", "preflight-nonrepo"))
	if err != nil {
		b.Fatal(err)
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		b.Fatal(err)
	}
	root, err := os.MkdirTemp(parent, "case-")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = os.RemoveAll(root) })
	b.Setenv("GIT_CEILING_DIRECTORIES", parent)
	if err := os.WriteFile(filepath.Join(root, "source.go"), []byte("local source\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	var calls atomic.Int64
	options := Options{RunGit: func(root string, args ...string) (string, error) {
		calls.Add(1)
		ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
		return realRunGit(ctx, root, args...)
	}}
	block := Build(root, options)
	if !strings.Contains(block, "source.go") || strings.Contains(block, "branch:") {
		b.Fatalf("non-repository fixture was not isolated: %q", block)
	}
	calls.Store(0)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		preflightBriefingSink = Build(root, options)
		if preflightBriefingSink != block {
			b.Fatal("immutable fallback fixture produced changed briefing")
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(calls.Load())/float64(b.N), "git-calls/op")
	b.ReportMetric(float64(EstimateTokens(block)), "estimated-tokens")
}
