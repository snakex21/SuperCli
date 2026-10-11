package preflight

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Both sides run actual Git in the same immutable portable fixture. The only
// variable is the static log loader: a gated cold group guarantees overlap.
// Each caller owns its fresh status process and deadline. Original Build
// assembles the same collected bytes through a subprocess-free explicit runner;
// the reported command counters exclude those assembly callbacks.
// This is a native collection/assembly benchmark, not a whole agent turn or
// provider/model-call benchmark. A second measurement covers full native Build.
func nativeGitLogBurst(tb testing.TB, root, executable string, size int, candidate bool, loader gitLogLoader, statusCalls, logCalls *atomic.Int64) []string {
	tb.Helper()
	gitStaticCache.Delete(gitLogFlightKey{filepath.Clean(root), executable})
	legacyGitLogCache.Delete(filepath.Clean(root))
	started, waiting := make(chan struct{}, size), make(chan struct{}, size)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	type result struct {
		block string
		err   error
	}
	results := make(chan result, size)
	invoke := func(follower bool) {
		ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
		var logCtx context.Context = ctx
		if follower {
			logCtx = &gitLogWaitingContext{Context: ctx, waiting: waiting}
		}
		run := func(gotRoot string, args ...string) (string, error) {
			if len(args) != 0 && args[0] == "log" {
				logCalls.Add(1)
				started <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
					return "", ctx.Err()
				}
			} else if len(args) != 0 && args[0] == "status" {
				statusCalls.Add(1)
			}
			return realRunGitExecutable(ctx, executable, gotRoot, args...)
		}
		statusDone := make(chan struct{})
		var status string
		var statusErr error
		go func() {
			defer close(statusDone)
			status, statusErr = run(root, "status", "--porcelain", "--branch")
		}()
		head, lg := loader(logCtx, root, executable, run, true)
		<-statusDone
		if statusErr != nil || head == "" || lg == "" {
			results <- result{err: fmt.Errorf("native fixture read failed: status=%v head=%q", statusErr, head)}
			return
		}
		block := Build(root, Options{LookPath: hasGit, RunGit: func(_ string, args ...string) (string, error) {
			switch args[0] {
			case "status":
				return status, nil
			case "log":
				return lg, nil
			default:
				return "", fmt.Errorf("unexpected assembly-only Git callback: %v", args)
			}
		}})
		results <- result{block: block}
	}
	go invoke(false)
	<-started
	for i := 1; i < size; i++ {
		go invoke(true)
	}
	for i := 1; i < size; i++ {
		if candidate {
			<-waiting
		} else {
			<-started
		}
	}
	releaseOnce.Do(func() { close(release) })
	blocks := make([]string, size)
	for i := range blocks {
		got := <-results
		if got.err != nil {
			tb.Fatal(got.err)
		}
		blocks[i] = got.block
	}
	return blocks
}

func BenchmarkPreflightNativeLogBurst(b *testing.B) {
	for _, size := range []int{4, 8} {
		for _, candidate := range []bool{false, true} {
			name := "legacy"
			loader := gitLogLoader(legacyLoadGitLog)
			if candidate {
				name, loader = "coalesced", loadGitLog
			}
			b.Run(fmt.Sprintf("group_%d/%s", size, name), func(b *testing.B) {
				root := preflightNativeFixture(b, 6)
				executable, err := exec.LookPath("git")
				if err != nil {
					b.Fatal(err)
				}
				want := Build(root, Options{Now: time.Unix(1, 0)})
				var statusCalls, logCalls atomic.Int64
				b.ReportAllocs()
				for b.Loop() {
					blocks := nativeGitLogBurst(b, root, executable, size, candidate, loader, &statusCalls, &logCalls)
					for _, block := range blocks {
						if block != want {
							b.Fatalf("native collection changed briefing bytes: got %q want %q", block, want)
						}
					}
				}
				wantLogs := int64(b.N * size)
				if candidate {
					wantLogs = int64(b.N)
				}
				if statusCalls.Load() != int64(b.N*size) || logCalls.Load() != wantLogs {
					b.Fatalf("unexpected native counts: status=%d log=%d", statusCalls.Load(), logCalls.Load())
				}
				b.ReportMetric(float64(statusCalls.Load()+logCalls.Load())/float64(b.N), "git-calls/group")
				b.ReportMetric(float64(logCalls.Load())/float64(b.N), "log-calls/group")
				b.ReportMetric(float64(statusCalls.Load())/float64(b.N), "status-calls/group")
			})
		}
	}
}

// Preserve a separate full production collector measurement. The native
// scheduler may let some cold requests finish before others start; subprocess
// counts are established by the gated benchmark above, not inferred here.
func BenchmarkPreflightNativeBuildBurst(b *testing.B) {
	for _, size := range []int{4, 8} {
		b.Run(fmt.Sprintf("group_%d", size), func(b *testing.B) {
			root := preflightNativeFixture(b, 6)
			executable, err := exec.LookPath("git")
			if err != nil {
				b.Fatal(err)
			}
			want := Build(root, Options{Now: time.Unix(1, 0)})
			b.ReportAllocs()
			for b.Loop() {
				gitStaticCache.Delete(gitLogFlightKey{filepath.Clean(root), executable})
				results := make(chan string, size)
				start := make(chan struct{})
				for i := 0; i < size; i++ {
					go func() {
						<-start
						results <- Build(root, Options{})
					}()
				}
				close(start)
				for i := 0; i < size; i++ {
					if block := <-results; block != want {
						b.Fatalf("full production collector changed bytes: got %q want %q", block, want)
					}
				}
			}
		})
	}
}

func BenchmarkPreflightLogWarmBookkeeping(b *testing.B) {
	for _, candidate := range []bool{false, true} {
		name := "legacy"
		loader := gitLogLoader(legacyLoadGitLog)
		cache := &legacyGitLogCache
		if candidate {
			name, loader, cache = "coalesced", loadGitLog, &gitStaticCache
		}
		b.Run(name, func(b *testing.B) {
			root := b.TempDir()
			var key any = filepath.Clean(root)
			if candidate {
				key = gitLogFlightKey{filepath.Clean(root), "resolved-git"}
			}
			// Force only warm hits even when a caller runs a long benchmark.
			cache.Store(key, gitStaticState{at: time.Now().Add(time.Hour), head: "abc first", log: "abc first\ndef second"})
			run := func(string, ...string) (string, error) {
				b.Fatal("warm-only fixture unexpectedly started Git")
				return "", nil
			}
			b.ReportAllocs()
			for b.Loop() {
				head, lg := loader(context.Background(), root, "resolved-git", run, true)
				if head != "abc first" || lg != "abc first\ndef second" {
					b.Fatal("warm facts changed")
				}
			}
		})
	}
}
