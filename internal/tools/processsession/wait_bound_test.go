package processsession

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func boundWaitTimings(t *testing.T, max, stall, check time.Duration) {
	t.Helper()
	savedMax, savedStall, savedCheck := waitDefaultMax, waitStallAfter, waitStallCheck
	waitDefaultMax, waitStallAfter, waitStallCheck = max, stall, check
	t.Cleanup(func() { waitDefaultMax, waitStallAfter, waitStallCheck = savedMax, savedStall, savedCheck })
}

// Audited: a worker waited 49 minutes on a child that used 0.5 s of CPU until
// the 150-minute run limit killed the whole run. A silent process is now
// reported as looking hung, and the process is left running.
func TestWaitReportsAHungProcess(t *testing.T) {
	boundWaitTimings(t, time.Minute, 120*time.Millisecond, 20*time.Millisecond)
	_, item, reg := waitFixture(t)
	started := time.Now()
	r, err := reg.Execute(context.Background(), "process_session", json.RawMessage(`{"action":"wait","id":"proc-1"}`))
	if err != nil || r.Err != nil {
		t.Fatalf("wait: %v %v", err, r.Err)
	}
	var s snapshot
	if err := json.Unmarshal([]byte(r.Text), &s); err != nil {
		t.Fatal(err)
	}
	if !s.Hung || s.Status != "running" || s.WaitNote == "" || time.Since(started) > 5*time.Second {
		t.Fatalf("hung process not reported: %+v", s)
	}
	select {
	case <-item.done:
		t.Fatal("a bounded wait must not stop the process")
	default:
	}
}

// A process that keeps printing is not "hung"; the wait still ends at its
// limit with a still-running snapshot instead of blocking for hours.
func TestWaitReturnsStillRunningAtItsLimit(t *testing.T) {
	boundWaitTimings(t, time.Hour, 150*time.Millisecond, 20*time.Millisecond)
	_, item, reg := waitFixture(t)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(10 * time.Millisecond):
				_, _ = item.stdout.Write([]byte("compiling...\n"))
			}
		}
	}()
	started := time.Now()
	r, err := reg.Execute(context.Background(), "process_session", json.RawMessage(`{"action":"wait","id":"proc-1","wait_ms":400}`))
	if err != nil || r.Err != nil {
		t.Fatalf("wait: %v %v", err, r.Err)
	}
	var s snapshot
	if err := json.Unmarshal([]byte(r.Text), &s); err != nil {
		t.Fatal(err)
	}
	took := time.Since(started)
	if s.Hung || s.Status != "running" || s.WaitNote == "" || s.Stdout == "" || took < 350*time.Millisecond || took > 5*time.Second {
		t.Fatalf("limit not applied: took=%s %+v", took, s)
	}
	if waitLimit(0) != time.Hour || waitLimit(int((5*time.Hour)/time.Millisecond)) != waitMaxLimit {
		t.Fatal("wait_ms default/maximum")
	}
}
