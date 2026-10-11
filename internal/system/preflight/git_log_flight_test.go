package preflight

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type gitLogPair struct{ head, log string }

// Done is evaluated when loadGitLog reaches its follower select. This lets
// tests release the leader only after all followers have joined, without
// sleeps, status polling or production test hooks.
type gitLogWaitingContext struct {
	context.Context
	waiting chan<- struct{}
	once    sync.Once
}

func (ctx *gitLogWaitingContext) Done() <-chan struct{} {
	ctx.once.Do(func() { ctx.waiting <- struct{}{} })
	return ctx.Context.Done()
}

type gitLogLoader func(context.Context, string, string, func(string, ...string) (string, error), bool) (string, string)

// Independent pre-edit oracle. Its completed cache, error/output handling and
// timestamp are frozen separately from the candidate's flight implementation.
var legacyGitLogCache sync.Map

func legacyLoadGitLog(_ context.Context, root, _ string, runGit func(string, ...string) (string, error), cacheable bool) (head, lg string) {
	key := filepath.Clean(root)
	if cacheable {
		if cached, ok := legacyGitLogCache.Load(key); ok {
			state := cached.(gitStaticState)
			if time.Since(state.at) < gitStaticCacheTTL {
				return state.head, state.log
			}
		}
	}
	lg, _ = runGit(root, "log", "--oneline", "-"+itoa(defaultMaxCommits))
	if lines := splitLines(lg); len(lines) > 0 {
		head = lines[0]
	}
	if cacheable && head != "" {
		legacyGitLogCache.Store(key, gitStaticState{at: time.Now(), head: head, log: lg})
	}
	return head, lg
}

func loadGitLogAsync(ctx context.Context, root, executable string, run func(string, ...string) (string, error)) <-chan gitLogPair {
	done := make(chan gitLogPair, 1)
	go func() {
		head, lg := loadGitLog(ctx, root, executable, run, true)
		done <- gitLogPair{head, lg}
	}()
	return done
}

func assertGitLogFlightReleased(t *testing.T, root, executable string) {
	t.Helper()
	gitLogFlights.Lock()
	_, exists := gitLogFlights.m[gitLogFlightKey{filepath.Clean(root), executable}]
	gitLogFlights.Unlock()
	if exists {
		t.Fatal("completed log retained an in-flight entry")
	}
}

func TestGitLogFlightColdAndExpiredBurstMatchesLegacy(t *testing.T) {
	for _, expired := range []bool{false, true} {
		for _, size := range []int{4, 8} {
			t.Run(fmt.Sprintf("expired_%t/group_%d", expired, size), func(t *testing.T) {
				for _, candidate := range []bool{false, true} {
					root := filepath.Join(t.TempDir(), fmt.Sprint(candidate))
					var key any = filepath.Clean(root)
					cache, loader := &legacyGitLogCache, gitLogLoader(legacyLoadGitLog)
					if candidate {
						cache, loader = &gitStaticCache, loadGitLog
						key = gitLogFlightKey{filepath.Clean(root), "resolved-git"}
					}
					if expired {
						cache.Store(key, gitStaticState{at: time.Now().Add(-gitStaticCacheTTL - time.Second), head: "old", log: "old"})
					}
					started := make(chan struct{}, size)
					waiting := make(chan struct{}, size)
					release := make(chan struct{})
					var releaseOnce sync.Once
					t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
					var calls atomic.Int64
					run := func(gotRoot string, args ...string) (string, error) {
						if gotRoot != root || strings.Join(args, " ") != "log --oneline -8" {
							t.Errorf("changed runner invocation: %q %v", gotRoot, args)
						}
						calls.Add(1)
						started <- struct{}{}
						<-release
						return "abc123 first\ndef456 second", nil
					}
					results := make(chan gitLogPair, size)
					invoke := func(ctx context.Context) {
						head, lg := loader(ctx, root, "resolved-git", run, true)
						results <- gitLogPair{head, lg}
					}
					go invoke(context.Background())
					<-started
					for i := 1; i < size; i++ {
						go invoke(&gitLogWaitingContext{Context: context.Background(), waiting: waiting})
					}
					for i := 1; i < size; i++ {
						if candidate {
							<-waiting
						} else {
							<-started
						}
					}
					wantCalls := int64(size)
					if candidate {
						wantCalls = 1
					}
					if got := calls.Load(); got != wantCalls {
						t.Errorf("candidate=%t log calls=%d, want %d", candidate, got, wantCalls)
					}
					releaseOnce.Do(func() { close(release) })
					for i := 0; i < size; i++ {
						if got := <-results; got != (gitLogPair{"abc123 first", "abc123 first\ndef456 second"}) {
							t.Errorf("candidate=%t changed complete facts: %+v", candidate, got)
						}
					}
					if candidate {
						assertGitLogFlightReleased(t, root, "resolved-git")
					}
				}
			})
		}
	}
}

func TestGitLogFlightWarmHitsDoNotRenewTTL(t *testing.T) {
	root := t.TempDir()
	key := gitLogFlightKey{filepath.Clean(root), "resolved-git"}
	// Force warm hits independently of pauses in the test runner; expiry below
	// is injected explicitly rather than waiting for a wall-clock boundary.
	state := gitStaticState{at: time.Now().Add(time.Minute), head: "abc first", log: "abc first\ndef second"}
	gitStaticCache.Store(key, state)
	for i := 0; i < 3; i++ {
		head, lg := loadGitLog(context.Background(), root, "resolved-git", func(string, ...string) (string, error) {
			t.Fatal("warm hit started another Git process")
			return "", nil
		}, true)
		if head != state.head || lg != state.log {
			t.Fatalf("changed cached facts: %q %q", head, lg)
		}
	}
	if got, _ := gitStaticCache.Load(key); got != state {
		t.Fatalf("hits changed original cache timestamp/state: %+v", got)
	}
	state.at = time.Now().Add(-gitStaticCacheTTL - time.Second)
	gitStaticCache.Store(key, state)
	var calls int
	for i := 0; i < 2; i++ {
		head, _ := loadGitLog(context.Background(), root, "resolved-git", func(string, ...string) (string, error) {
			calls++
			return "new current", nil
		}, true)
		if head != "new current" {
			t.Fatal("expired state remained visible")
		}
	}
	if calls != 1 {
		t.Fatalf("expired then warm calls=%d, want 1", calls)
	}
}

func TestGitLogFlightCanceledFollowerDoesNotCancelLeader(t *testing.T) {
	root := t.TempDir()
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	run := func(string, ...string) (string, error) {
		calls.Add(1)
		close(started)
		<-release
		return "complete intact", nil
	}
	leader := loadGitLogAsync(context.Background(), root, "resolved-git", run)
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	waiting := make(chan struct{}, 1)
	follower := loadGitLogAsync(&gitLogWaitingContext{Context: ctx, waiting: waiting}, root, "resolved-git", run)
	<-waiting
	cancel()
	if got := <-follower; got != (gitLogPair{}) {
		t.Fatalf("canceled follower returned facts: %+v", got)
	}
	close(release)
	if got := <-leader; got != (gitLogPair{"complete intact", "complete intact"}) || calls.Load() != 1 {
		t.Fatalf("follower canceled or duplicated leader: result=%+v calls=%d", got, calls.Load())
	}
	assertGitLogFlightReleased(t, root, "resolved-git")
}

func TestGitLogFlightHealthyFollowerRetriesCanceledLeader(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	leader := loadGitLogAsync(ctx, root, "resolved-git", func(string, ...string) (string, error) {
		close(started)
		<-ctx.Done()
		// Process cancellation may surface as an OS exit error (or a runner
		// may return nil), so caller cancellation must independently win.
		return "partial canceled facts", nil
	})
	<-started
	waiting := make(chan struct{}, 1)
	var calls atomic.Int64
	follower := loadGitLogAsync(&gitLogWaitingContext{Context: context.Background(), waiting: waiting}, root, "resolved-git", func(string, ...string) (string, error) {
		calls.Add(1)
		return "healthy complete facts", nil
	})
	<-waiting
	cancel()
	if got := <-leader; got != (gitLogPair{}) {
		t.Fatalf("published leader's canceled partial facts: %+v", got)
	}
	if got := <-follower; got != (gitLogPair{"healthy complete facts", "healthy complete facts"}) || calls.Load() != 1 {
		t.Fatalf("healthy follower did not retry: %+v calls=%d", got, calls.Load())
	}
	state, ok := gitStaticCache.Load(gitLogFlightKey{filepath.Clean(root), "resolved-git"})
	if !ok || state.(gitStaticState).head != "healthy complete facts" {
		t.Fatalf("canceled partial state poisoned completed cache: %+v", state)
	}
	assertGitLogFlightReleased(t, root, "resolved-git")
}

func TestGitLogFlightCancellationErrorsDoNotCachePartialState(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			root := t.TempDir()
			head, lg := loadGitLog(context.Background(), root, "resolved-git", func(string, ...string) (string, error) {
				return "partial", fmt.Errorf("git interrupted: %w", cause)
			}, true)
			if head != "" || lg != "" {
				t.Fatalf("cancellation error published partial facts: %q %q", head, lg)
			}
			if _, exists := gitStaticCache.Load(gitLogFlightKey{filepath.Clean(root), "resolved-git"}); exists {
				t.Fatal("cancellation error cached partial state")
			}
			assertGitLogFlightReleased(t, root, "resolved-git")
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if head, lg := loadGitLog(ctx, t.TempDir(), "resolved-git", func(string, ...string) (string, error) {
		t.Fatal("already canceled native collection started a probe")
		return "", nil
	}, true); head != "" || lg != "" {
		t.Fatal("already canceled collection returned facts")
	}
}

func TestGitLogFlightOrdinaryErrorsAndEmptyResultsRemainRetryable(t *testing.T) {
	for _, cause := range []error{nil, errors.New("ordinary Git failure")} {
		root := t.TempDir()
		started, release := make(chan struct{}), make(chan struct{})
		var calls atomic.Int64
		run := func(string, ...string) (string, error) {
			if calls.Add(1) == 1 {
				close(started)
				<-release
			}
			return "", cause
		}
		leader := loadGitLogAsync(context.Background(), root, "resolved-git", run)
		<-started
		waiting := make(chan struct{}, 1)
		follower := loadGitLogAsync(&gitLogWaitingContext{Context: context.Background(), waiting: waiting}, root, "resolved-git", run)
		<-waiting
		close(release)
		if a, b := <-leader, <-follower; a != (gitLogPair{}) || b != a || calls.Load() != 1 {
			t.Fatalf("changed overlapping negative/empty handling: %+v %+v calls=%d", a, b, calls.Load())
		}
		loadGitLog(context.Background(), root, "resolved-git", run, true)
		if calls.Load() != 2 {
			t.Fatal("completed negative/empty result was persistently cached")
		}
		assertGitLogFlightReleased(t, root, "resolved-git")
	}
	// Preserve the pre-edit treatment of output from an ordinary failure;
	// only cancellation makes partial output ineligible for sharing/caching.
	root := t.TempDir()
	wantHead, wantLog := legacyLoadGitLog(context.Background(), root, "resolved-git", func(string, ...string) (string, error) {
		return "abc known output", errors.New("ordinary exit error")
	}, true)
	gotHead, gotLog := loadGitLog(context.Background(), root, "resolved-git", func(string, ...string) (string, error) {
		return "abc known output", errors.New("ordinary exit error")
	}, true)
	if gotHead != wantHead || gotLog != wantLog {
		t.Fatalf("ordinary-error output changed: %q %q vs %q %q", gotHead, gotLog, wantHead, wantLog)
	}
}

func TestGitLogFlightRootAndExecutableIsolation(t *testing.T) {
	root := t.TempDir()
	firstStarted, release := make(chan struct{}), make(chan struct{})
	leader := loadGitLogAsync(context.Background(), root, "first-git", func(string, ...string) (string, error) {
		close(firstStarted)
		<-release
		return "first root", nil
	})
	<-firstStarted
	for _, other := range []struct{ root, executable string }{{t.TempDir(), "first-git"}, {root, "second-git"}} {
		if got := <-loadGitLogAsync(context.Background(), other.root, other.executable, func(string, ...string) (string, error) {
			return "independent root or executable", nil
		}); got.head != "independent root or executable" {
			t.Fatal("unrelated root or resolved executable joined another flight")
		}
	}
	close(release)
	<-leader
	assertGitLogFlightReleased(t, root, "first-git")
}

func TestGitLogFlightWarmCacheSeparatesExecutables(t *testing.T) {
	root := t.TempDir()
	firstKey := gitLogFlightKey{filepath.Clean(root), "first-git"}
	firstCalls, secondCalls := 0, 0
	firstRun := func(string, ...string) (string, error) {
		firstCalls++
		return "first executable facts", nil
	}
	wantFirst := gitLogPair{"first executable facts", "first executable facts"}
	if head, lg := loadGitLog(context.Background(), root, "first-git", firstRun, true); (gitLogPair{head, lg}) != wantFirst {
		t.Fatal("first executable lost its collected facts")
	}
	cached, exists := gitStaticCache.Load(firstKey)
	if !exists {
		t.Fatal("completed facts were not stored under their executable identity")
	}
	// Keep A warm independently of pauses in a loaded test environment. B
	// must collect its own facts rather than using or renewing A's timestamp.
	firstState := cached.(gitStaticState)
	firstState.at = time.Now().Add(time.Minute)
	gitStaticCache.Store(firstKey, firstState)
	head, lg := loadGitLog(context.Background(), root, "second-git", func(string, ...string) (string, error) {
		secondCalls++
		return "second executable facts", nil
	}, true)
	if (gitLogPair{head, lg}) != (gitLogPair{"second executable facts", "second executable facts"}) || secondCalls != 1 {
		t.Fatal("second executable consumed the first executable's warm facts")
	}
	head, lg = loadGitLog(context.Background(), root, "first-git", firstRun, true)
	if (gitLogPair{head, lg}) != wantFirst || firstCalls != 1 {
		t.Fatal("second executable displaced the first executable's warm facts")
	}
	if current, _ := gitStaticCache.Load(firstKey); current != firstState {
		t.Fatal("another executable altered the first cache entry or renewed its TTL")
	}
	assertGitLogFlightReleased(t, root, "first-git")
	assertGitLogFlightReleased(t, root, "second-git")
}

func TestGitLogFlightPendingSourceIgnoresOtherExecutableCompletedCache(t *testing.T) {
	root := t.TempDir()
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	var firstCalls, followerCalls atomic.Int64
	leader := loadGitLogAsync(context.Background(), root, "first-git", func(string, ...string) (string, error) {
		firstCalls.Add(1)
		close(started)
		<-release
		return "first executable completed facts", nil
	})
	<-started
	head, lg := loadGitLog(context.Background(), root, "second-git", func(string, ...string) (string, error) {
		return "second executable completed facts", nil
	}, true)
	if (gitLogPair{head, lg}) != (gitLogPair{"second executable completed facts", "second executable completed facts"}) {
		t.Fatal("independent executable joined the pending first executable")
	}
	waiting := make(chan struct{}, 1)
	follower := loadGitLogAsync(&gitLogWaitingContext{Context: context.Background(), waiting: waiting}, root, "first-git", func(string, ...string) (string, error) {
		followerCalls.Add(1)
		return "unexpected duplicate first process", nil
	})
	// Await joining or erroneous completion directly; no timeout sleep or
	// progress polling is needed to distinguish the cache/flight boundary.
	select {
	case <-waiting:
	case got := <-follower:
		t.Fatalf("first executable's follower consumed another source's cache or ran independently: %+v", got)
	}
	releaseOnce.Do(func() { close(release) })
	want := gitLogPair{"first executable completed facts", "first executable completed facts"}
	if a, b := <-leader, <-follower; a != want || b != want || firstCalls.Load() != 1 || followerCalls.Load() != 0 {
		t.Fatalf("pending same-source coalescing changed: leader=%+v follower=%+v first=%d duplicate=%d", a, b, firstCalls.Load(), followerCalls.Load())
	}
	assertGitLogFlightReleased(t, root, "first-git")
	assertGitLogFlightReleased(t, root, "second-git")
}

func TestGitLogFlightInjectedRunnerRemainsIndependent(t *testing.T) {
	root := t.TempDir()
	gitStaticCache.Store(gitLogFlightKey{filepath.Clean(root), "resolved-git"}, gitStaticState{at: time.Now(), head: "native cached", log: "native cached"})
	var calls int
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 2; i++ {
		head, lg := loadGitLog(ctx, root, "resolved-git", func(string, ...string) (string, error) {
			calls++
			return "injected own facts", nil
		}, false)
		if head != "injected own facts" || lg != head {
			t.Fatal("native cache/cancellation changed explicit-runner behavior")
		}
	}
	if calls != 2 {
		t.Fatalf("explicit runner calls=%d, want 2", calls)
	}
}

func TestGitLogFlightCapacityIsBoundedAndReleased(t *testing.T) {
	root := t.TempDir()
	started := make(chan struct{}, maxGitLogFlights)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	leaders := make([]<-chan gitLogPair, maxGitLogFlights)
	for i := range leaders {
		leaders[i] = loadGitLogAsync(context.Background(), filepath.Join(root, fmt.Sprint(i)), "resolved-git", func(string, ...string) (string, error) {
			started <- struct{}{}
			<-release
			return "complete bounded facts", nil
		})
	}
	for range leaders {
		<-started
	}
	gitLogFlights.Lock()
	count := len(gitLogFlights.m)
	gitLogFlights.Unlock()
	if count != maxGitLogFlights {
		t.Errorf("active entry count=%d, want %d", count, maxGitLogFlights)
	}
	overflowRoot := filepath.Join(root, "overflow")
	if got := <-loadGitLogAsync(context.Background(), overflowRoot, "resolved-git", func(string, ...string) (string, error) {
		return "independent overflow facts", nil
	}); got.head != "independent overflow facts" {
		t.Fatal("overflow did not retain independent native collection")
	}
	assertGitLogFlightReleased(t, overflowRoot, "resolved-git")
	releaseOnce.Do(func() { close(release) })
	for i, done := range leaders {
		<-done
		assertGitLogFlightReleased(t, filepath.Join(root, fmt.Sprint(i)), "resolved-git")
	}
}

func TestGitLogFlightPanicReleasesHealthyFollower(t *testing.T) {
	root := t.TempDir()
	started, release, recovered := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer func() { _ = recover(); close(recovered) }()
		loadGitLog(context.Background(), root, "resolved-git", func(string, ...string) (string, error) {
			close(started)
			<-release
			panic("fixture failure")
		}, true)
	}()
	<-started
	waiting := make(chan struct{}, 1)
	follower := loadGitLogAsync(&gitLogWaitingContext{Context: context.Background(), waiting: waiting}, root, "resolved-git", func(string, ...string) (string, error) {
		return "complete recovery facts", nil
	})
	<-waiting
	close(release)
	<-recovered
	if got := <-follower; got.head != "complete recovery facts" {
		t.Fatal("panicking leader retained a stuck flight")
	}
	assertGitLogFlightReleased(t, root, "resolved-git")
}

// Native subprocess contract test: the same log loader used by BuildContext,
// one real status per caller, and original briefing assembly from collected
// bytes. The assembly's explicit runner performs no additional subprocesses.
func TestGitLogFlightNativeBurstKeepsExactBriefingAndFreshStatus(t *testing.T) {
	for _, size := range []int{4, 8} {
		t.Run(fmt.Sprintf("group_%d", size), func(t *testing.T) {
			root := preflightNativeFixture(t, 6)
			executable, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			want := Build(root, Options{Now: time.Unix(1, 0)})
			var statusCalls, logCalls atomic.Int64
			blocks := nativeGitLogBurst(t, root, executable, size, true, loadGitLog, &statusCalls, &logCalls)
			for _, got := range blocks {
				if got != want {
					t.Fatalf("shared native read changed briefing bytes: got %q want %q", got, want)
				}
			}
			if statusCalls.Load() != int64(size) || logCalls.Load() != 1 {
				t.Fatalf("native status/log=%d/%d, want %d/1", statusCalls.Load(), logCalls.Load(), size)
			}
			if err := os.WriteFile(filepath.Join(root, "tracked.go"), []byte("fresh worktree modification\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			// The shared completed log is still warm; actual production builds
			// must see immediate edits without changing its timestamp.
			key := gitLogFlightKey{filepath.Clean(root), executable}
			cached, _ := gitStaticCache.Load(key)
			state := cached.(gitStaticState)
			// Keep this fixture warm regardless of slow CI subprocess startup.
			// TTL expiration itself is covered without real-time waiting above.
			state.at = time.Now().Add(time.Minute)
			gitStaticCache.Store(key, state)
			for i := 0; i < size; i++ {
				if block := Build(root, Options{}); !strings.Contains(block, " M tracked.go") {
					t.Fatalf("native warm build hid fresh status: %q", block)
				}
			}
			current, _ := gitStaticCache.Load(key)
			if current != state {
				t.Fatal("production warm hits renewed log TTL")
			}
		})
	}
}
