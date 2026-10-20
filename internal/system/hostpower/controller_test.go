package hostpower

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct {
	mu        sync.Mutex
	now       time.Time
	callbacks []func()
	delays    []time.Duration
	stops     int
}

func newClock() *fakeClock          { return &fakeClock{now: time.Unix(1700000000, 0)} }
func (f *fakeClock) Now() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.now }
func (f *fakeClock) After(d time.Duration, fn func()) func() {
	f.mu.Lock()
	f.callbacks = append(f.callbacks, fn)
	f.delays = append(f.delays, d)
	f.mu.Unlock()
	// Deliberately permit delivery after cancellation to exercise stale callbacks.
	return func() { f.mu.Lock(); f.stops++; f.mu.Unlock() }
}
func (f *fakeClock) advance(d time.Duration) { f.mu.Lock(); f.now = f.now.Add(d); f.mu.Unlock() }
func (f *fakeClock) fire(i int)              { f.mu.Lock(); fn := f.callbacks[i]; f.mu.Unlock(); fn() }
func fixture(t *testing.T) (*Controller, *fakeClock, *atomic.Int32) {
	t.Helper()
	clock, calls := newClock(), new(atomic.Int32)
	c := New(Options{Execute: func(ctx context.Context) error {
		if ctx.Err() != nil {
			t.Errorf("execution context cancelled: %v", ctx.Err())
		}
		calls.Add(1)
		return nil
	}, Now: clock.Now, AfterFunc: clock.After})
	t.Cleanup(c.Close)
	return c, clock, calls
}
func arm(t *testing.T, c *Controller, check func() error) {
	t.Helper()
	c.Begin("job")
	if err := c.Arm("job", "goal", "Trusted title", check); err != nil {
		t.Fatal(err)
	}
}
func wantStatus(t *testing.T, c *Controller, want string) {
	t.Helper()
	if s := c.Snapshot(); s.Status != want {
		t.Fatalf("snapshot = %+v; want %s", s, want)
	}
}

func TestVerifiedOneShot(t *testing.T) {
	c, clock, calls := fixture(t)
	var checks atomic.Int32
	arm(t, c, func() error { checks.Add(1); return nil })
	wantStatus(t, c, "armed")
	c.Finish("unrelated", true)
	wantStatus(t, c, "armed")
	c.Finish("job", true)
	wantStatus(t, c, "countdown")
	if s := c.Snapshot(); s.Deadline != clock.Now().Add(time.Minute).UnixMilli() || s.Goal != "Trusted title" || s.JobID != "job" || s.GoalID != "goal" {
		t.Fatal(s)
	}
	if calls.Load() != 0 || checks.Load() != 1 {
		t.Fatal("executed before deadline or missing finish recheck")
	}
	clock.advance(time.Minute)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() { clock.fire(0) })
	}
	wg.Wait()
	wantStatus(t, c, "executed")
	if calls.Load() != 1 {
		t.Fatalf("executions = %d", calls.Load())
	}
	c.Finish("job", true)
	clock.fire(0)
	if calls.Load() != 1 {
		t.Fatal("authorization was reused")
	}
	if checks.Load() < 2 {
		t.Fatal("missing deadline recheck")
	}
}

func TestInvalidArming(t *testing.T) {
	cases := []struct {
		name             string
		setup            func(*Controller)
		job, goal, title string
		check            func() error
	}{
		{"inactive", func(*Controller) {}, "job", "goal", "title", func() error { return nil }},
		{"other roots", func(c *Controller) { c.Begin("job"); c.Begin("other") }, "job", "goal", "title", func() error { return nil }},
		{"missing check", func(c *Controller) { c.Begin("job") }, "job", "goal", "title", nil},
		{"missing goal", func(c *Controller) { c.Begin("job") }, "job", "", "title", func() error { return nil }},
		{"missing title", func(c *Controller) { c.Begin("job") }, "job", "goal", " ", func() error { return nil }},
		{"closed", func(c *Controller) { c.Begin("job"); c.Close() }, "job", "goal", "title", func() error { return nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _, calls := fixture(t)
			tc.setup(c)
			if err := c.Arm(tc.job, tc.goal, tc.title, tc.check); err == nil {
				t.Fatal("arming unexpectedly allowed")
			}
			c.Finish(tc.job, true)
			if calls.Load() != 0 {
				t.Fatal("executed invalid authorization")
			}
		})
	}
	c := New(Options{})
	defer c.Close()
	c.Begin("job")
	if err := c.Arm("job", "goal", "title", func() error { return nil }); err == nil {
		t.Fatal("nil executor did not fail closed")
	}
	if c.opts.Delay != time.Minute {
		t.Fatal("incorrect default delay")
	}
}

func TestFinishBlocksFailureAndFailedRecheck(t *testing.T) {
	for _, success := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed run", true: "failed verification"}[success], func(t *testing.T) {
			c, clock, calls := fixture(t)
			arm(t, c, func() error { return errors.New("work incomplete") })
			c.Finish("job", success)
			wantStatus(t, c, "blocked")
			if c.Snapshot().Error == "" || len(clock.callbacks) != 0 || calls.Load() != 0 {
				t.Fatal("failure did not block")
			}
		})
	}
}

func TestDeadlineFreshRecheckAndExecutorFailure(t *testing.T) {
	c, clock, calls := fixture(t)
	fresh := true
	arm(t, c, func() error {
		if !fresh {
			return errors.New("stale")
		}
		return nil
	})
	c.Finish("job", true)
	fresh = false
	clock.advance(time.Minute)
	clock.fire(0)
	wantStatus(t, c, "blocked")
	if calls.Load() != 0 {
		t.Fatal("stale evidence executed")
	}
	clock = newClock()
	c = New(Options{Now: clock.Now, AfterFunc: clock.After, Execute: func(context.Context) error { return errors.New("permission denied") }})
	defer c.Close()
	arm(t, c, func() error { return nil })
	c.Finish("job", true)
	clock.advance(time.Minute)
	clock.fire(0)
	wantStatus(t, c, "failed")
	if c.Snapshot().Error != "permission denied" {
		t.Fatal(c.Snapshot())
	}
}

func TestRevocationDiscardsEvenDeliveredTimers(t *testing.T) {
	for _, action := range []string{"cancel", "close", "new root", "same root"} {
		t.Run(action, func(t *testing.T) {
			c, clock, calls := fixture(t)
			arm(t, c, func() error { return nil })
			c.Finish("job", true)
			switch action {
			case "cancel":
				c.Cancel()
			case "close":
				c.Close()
			case "new root":
				c.Begin("new")
			case "same root":
				c.Begin("job")
			}
			clock.advance(time.Minute)
			clock.fire(0)
			clock.fire(0)
			if calls.Load() != 0 || c.Snapshot().Deadline != 0 {
				t.Fatal(c.Snapshot())
			}
			if clock.stops != 1 {
				t.Fatalf("timer stops = %d", clock.stops)
			}
		})
	}
}

func TestNewRootCancelsArmedAndOtherRootCannotFinishIt(t *testing.T) {
	c, _, calls := fixture(t)
	arm(t, c, func() error { return nil })
	c.Begin("other")
	wantStatus(t, c, "cancelled")
	c.Finish("other", true)
	c.Finish("job", true)
	if calls.Load() != 0 {
		t.Fatal("scope change executed")
	}
}

func TestCancelDuringRecheck(t *testing.T) {
	for _, stage := range []string{"finish", "deadline"} {
		for _, revoke := range []string{"cancel", "close", "begin"} {
			t.Run(stage+"/"+revoke, func(t *testing.T) {
				c, clock, calls := fixture(t)
				entered, release := make(chan struct{}), make(chan struct{})
				var checks atomic.Int32
				arm(t, c, func() error {
					n := checks.Add(1)
					if stage == "finish" || n == 2 {
						close(entered)
						<-release
					}
					return nil
				})
				done := make(chan struct{})
				if stage == "finish" {
					go func() { c.Finish("job", true); close(done) }()
				} else {
					c.Finish("job", true)
					clock.advance(time.Minute)
					go func() { clock.fire(0); close(done) }()
				}
				<-entered
				switch revoke {
				case "cancel":
					c.Cancel()
				case "close":
					c.Close()
				case "begin":
					c.Begin("new")
				}
				close(release)
				<-done
				if calls.Load() != 0 {
					t.Fatal("revocation lost to in-flight recheck")
				}
				if s := c.Snapshot(); s.Status != "cancelled" || s.Deadline != 0 {
					t.Fatal(s)
				}
			})
		}
	}
}

func TestClaimedExecutorRetainsOutcomeAcrossLifecycleCalls(t *testing.T) {
	for _, action := range []string{"cancel", "begin", "close"} {
		for _, outcome := range []string{"executed", "failed"} {
			t.Run(action+"/"+outcome, func(t *testing.T) {
				clock := newClock()
				entered := make(chan context.Context, 1)
				release, finished := make(chan struct{}), make(chan struct{})
				var once sync.Once
				unblock := func() { once.Do(func() { close(release) }) }
				defer unblock()
				var calls atomic.Int32
				var c *Controller
				c = New(Options{Now: clock.Now, AfterFunc: clock.After, Execute: func(ctx context.Context) error {
					calls.Add(1)
					// Reentrant snapshots prove execution holds no controller lock.
					if c.Snapshot().Status != "executing" {
						t.Error("not executing")
					}
					entered <- ctx
					// Deliberately ignore cancellation, like an OS-accepted command.
					<-release
					if outcome == "failed" {
						return errors.New("executor outcome")
					}
					return nil
				}})
				defer c.Close()
				arm(t, c, func() error { _ = c.Snapshot(); return nil })
				c.Finish("job", true)
				clock.advance(time.Minute)
				go func() { clock.fire(0); close(finished) }()
				ctx := <-entered
				claimed, gen := c.Snapshot(), c.generation
				switch action {
				case "cancel":
					if s := c.Cancel(); s != claimed {
						t.Fatal("late cancel changed claimed snapshot", s)
					}
				case "begin":
					c.Begin("next")
					if !c.active["next"] {
						t.Fatal("new root was not recorded")
					}
				case "close":
					c.Close()
					if !c.closed {
						t.Fatal("controller was not closed")
					}
				}
				if s := c.Snapshot(); s != claimed || c.generation != gen || ctx.Err() != nil {
					t.Fatal("claimed execution was revoked", s, ctx.Err())
				}
				// Even a newly registered sole root cannot overwrite an executor.
				c.Begin("next")
				if err := c.Arm("next", "next-goal", "Next title", func() error { return nil }); err == nil {
					t.Fatal("Arm overwrote in-flight execution")
				}
				if s := c.Cancel(); s != claimed || c.generation != gen || ctx.Err() != nil {
					t.Fatal("repeated late cancellation changed execution", s)
				}
				unblock()
				<-finished
				result := c.Snapshot()
				if result.Status != outcome || result.JobID != claimed.JobID || result.GoalID != claimed.GoalID || result.Goal != claimed.Goal {
					t.Fatal("executor result was lost", result)
				}
				if (outcome == "failed" && result.Error != "executor outcome") || (outcome == "executed" && result.Error != "") {
					t.Fatal("executor error was lost", result)
				}
				if s := c.Cancel(); s != result {
					t.Fatal("terminal result changed on cancel", s)
				}
				c.Begin("later")
				c.Close()
				if s := c.Cancel(); s != result || c.generation != gen {
					t.Fatal("late lifecycle call hid terminal result", s)
				}
				clock.fire(0)
				if calls.Load() != 1 {
					t.Fatal("executor was claimed twice")
				}
			})
		}
	}
}

func TestEarlyCallbackBlocks(t *testing.T) {
	c, clock, calls := fixture(t)
	arm(t, c, func() error { return nil })
	c.Finish("job", true)
	clock.fire(0)
	wantStatus(t, c, "blocked")
	clock.advance(time.Minute)
	clock.fire(0)
	if calls.Load() != 0 {
		t.Fatal("early timer authorized execution")
	}
}

func TestSynchronousTimerAndRestart(t *testing.T) {
	clock := newClock()
	var calls atomic.Int32
	c := New(Options{Delay: time.Second, Now: clock.Now, AfterFunc: func(d time.Duration, fn func()) func() { clock.advance(d); fn(); return func() {} }, Execute: func(context.Context) error { calls.Add(1); return nil }})
	arm(t, c, func() error { return nil })
	c.Finish("job", true)
	wantStatus(t, c, "executed")
	c.Close()
	restarted := New(Options{Execute: func(context.Context) error { calls.Add(1); return nil }})
	defer restarted.Close()
	wantStatus(t, restarted, "idle")
	restarted.Finish("job", true)
	if calls.Load() != 1 {
		t.Fatal("authorization survived restart")
	}
}

func TestSnapshotJSONAndDuplicateArm(t *testing.T) {
	c, _, _ := fixture(t)
	arm(t, c, func() error { return nil })
	if err := c.Arm("job", "other", "other", func() error { return nil }); err == nil {
		t.Fatal("replaced approved goal without cancellation")
	}
	b, err := json.Marshal(c.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"status", "job_id", "goal_id", "goal", "deadline", "error"} {
		if _, ok := fields[key]; !ok {
			t.Fatal(key)
		}
	}
	if len(fields) != 6 {
		t.Fatal(fields)
	}
}

// NEVER call Shutdown from a test: only inspect the pure command plan.
func TestShutdownCommandPlans(t *testing.T) {
	for _, tc := range []struct {
		os, name string
		args     []string
	}{
		{"windows", "shutdown.exe", []string{"/s", "/t", "0"}},
		{"linux", "/sbin/shutdown", []string{"-h", "now"}},
		{"darwin", "/sbin/shutdown", []string{"-h", "now"}},
	} {
		t.Run(tc.os, func(t *testing.T) {
			name, args, err := shutdownPlan(tc.os)
			if err != nil || name != tc.name || !reflect.DeepEqual(args, tc.args) {
				t.Fatalf("%s %v %v", name, args, err)
			}
		})
	}
	if _, _, err := shutdownPlan("plan9"); err == nil {
		t.Fatal("unsupported OS allowed")
	}
}
