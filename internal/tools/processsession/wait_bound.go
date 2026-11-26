package processsession

import (
	"context"
	"fmt"
	"time"
)

// Bounded wait (after 1.0.41). wait used to block until the process exited,
// with no limit: a worker waiting on a hung child (49 minutes, 0.5 s of CPU)
// sat there until the hidden 150-minute run limit killed the whole worker
// run. wait now returns after at most wait_ms (default 15 minutes) with a
// "still running" snapshot, and earlier when the process shows no new output
// and no CPU use for waitStallAfter: then it says the process looks hung, so
// the worker can stop and retry it instead of waiting blindly.
var (
	waitDefaultMax = 15 * time.Minute
	waitMaxLimit   = 2 * time.Hour
	waitStallAfter = 10 * time.Minute
	waitStallCheck = 30 * time.Second
)

func waitLimit(ms int) time.Duration {
	if ms <= 0 {
		return waitDefaultMax
	}
	d := time.Duration(ms) * time.Millisecond
	if d > waitMaxLimit {
		d = waitMaxLimit
	}
	return d
}

// written is the total number of bytes the stream has received.
func (b *streamBuffer) written() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.base + int64(len(b.data))
}

// activity is what the hang check compares: output received and CPU used.
type activity struct {
	output int64
	cpu    time.Duration
	cpuOK  bool
}

func (p *process) activity() activity {
	a := activity{output: p.stdout.written() + p.stderr.written()}
	if p.pid > 0 {
		a.cpu, a.cpuOK = processCPUTime(p.pid)
	}
	return a
}

// WaitBounded waits for exit like Wait, but at most limit, and returns early
// when the process looks hung. The process is never stopped here.
func (m *Manager) WaitBounded(ctx context.Context, id string, limit time.Duration) (snapshot, error) {
	item, err := m.get(id)
	if err != nil {
		return snapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return snapshot{}, err
	}
	deadline := time.NewTimer(limit)
	defer deadline.Stop()
	check := time.NewTicker(waitStallCheck)
	defer check.Stop()
	last, lastChange := item.activity(), time.Now()
	for {
		select {
		case <-ctx.Done():
			return snapshot{}, ctx.Err()
		case <-item.done:
			return item.snapshot(maxBufferBytes), nil
		case <-deadline.C:
			s := item.snapshot(maxPollBytes)
			s.CPUMS = cpuMS(item.activity())
			s.WaitNote = fmt.Sprintf("Still running after waiting %s; this is its newest output. Call wait again to keep waiting, or stop it if it should have finished.", limit.Round(time.Second))
			return s, nil
		case <-check.C:
			now := item.activity()
			if now.output != last.output || (now.cpuOK && now.cpu-last.cpu > 50*time.Millisecond) || (now.cpuOK != last.cpuOK) {
				last, lastChange = now, time.Now()
				continue
			}
			if idle := time.Since(lastChange); idle >= waitStallAfter {
				s := item.snapshot(maxPollBytes)
				s.CPUMS = cpuMS(now)
				s.Hung = true
				cpu := "CPU use unknown"
				if now.cpuOK {
					cpu = fmt.Sprintf("total CPU %s", now.cpu.Round(10*time.Millisecond))
				}
				s.WaitNote = fmt.Sprintf("Process looks hung: no new output and no CPU use for %s (%s). It is still running; stop it and retry, or wait again if a child process is known to be working silently.", idle.Round(time.Second), cpu)
				return s, nil
			}
		}
	}
}

func cpuMS(a activity) int64 {
	if !a.cpuOK {
		return 0
	}
	return a.cpu.Milliseconds()
}
