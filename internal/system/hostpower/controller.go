// Package hostpower manages memory-only, one-shot host shutdown authorization.
// New never selects a live executor; production must explicitly supply Shutdown.
package hostpower

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// Options supplies execution and clock seams. Delay <= 0 means sixty seconds.
// AfterFunc returns a cancellation function; stale callbacks are safe even if
// cancellation cannot prevent delivery. Callbacks may run synchronously.
type Options struct {
	Execute   func(context.Context) error
	Delay     time.Duration
	Now       func() time.Time
	AfterFunc func(time.Duration, func()) func()
}

// Snapshot is a copy of the current state. Deadline is Unix milliseconds, or 0.
type Snapshot struct {
	Status   string `json:"status"`
	JobID    string `json:"job_id"`
	GoalID   string `json:"goal_id"`
	Goal     string `json:"goal"`
	Deadline int64  `json:"deadline"`
	Error    string `json:"error"`
}

// Controller is safe for concurrent use. Cancellation wins until execution is
// claimed under the mutex. Claim is irreversible: cancellation and scope changes
// cannot stop the executor or replace its eventual result with "cancelled".
type Controller struct {
	mu         sync.Mutex
	opts       Options
	active     map[string]bool
	generation uint64
	closed     bool
	state      Snapshot
	recheck    func() error
	stop       func()
}

func New(opts Options) *Controller {
	if opts.Delay <= 0 {
		opts.Delay = 60 * time.Second
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.AfterFunc == nil {
		opts.AfterFunc = func(d time.Duration, f func()) func() {
			t := time.AfterFunc(d, f)
			return func() { t.Stop() }
		}
	}
	return &Controller{opts: opts, active: make(map[string]bool), state: Snapshot{Status: "idle"}}
}

// invalidateLocked consumes any outstanding authorization. Run the returned
// cleanup outside the mutex, including user-supplied timer cancellation.
func (c *Controller) invalidateLocked() func() {
	c.generation++
	stop := c.stop
	c.stop, c.recheck = nil, nil
	c.state.Deadline = 0
	return func() {
		if stop != nil {
			stop()
		}
	}
}

// executionClaimedLocked includes terminal executor outcomes so late lifecycle
// calls cannot erase the result. A new explicit Arm may authorize a later job.
func (c *Controller) executionClaimedLocked() bool {
	return c.state.Status == "executing" || c.state.Status == "executed" || c.state.Status == "failed"
}

// Begin records a root job. Every Begin is a scope boundary, even when callers
// reuse an ID. Other active roots remain tracked until their matching Finish.
// A scope change revokes unclaimed authorization, never claimed execution.
func (c *Controller) Begin(jobID string) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	if strings.TrimSpace(jobID) != "" {
		c.active[jobID] = true
	}
	if c.executionClaimedLocked() {
		c.mu.Unlock()
		return
	}
	cleanup := c.invalidateLocked()
	if c.state.Status == "armed" || c.state.Status == "countdown" {
		c.state.Status, c.state.Error = "cancelled", "root job scope changed"
	}
	c.mu.Unlock()
	cleanup()
}

// Arm accepts authorization only after the caller has obtained trusted UI
// consent. The recheck must establish verified work, not turn completion.
func (c *Controller) Arm(jobID, goalID, title string, recheck func() error) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("host power controller is closed")
	}
	if c.opts.Execute == nil {
		c.mu.Unlock()
		return errors.New("host power executor is not configured")
	}
	if !c.active[jobID] || len(c.active) != 1 {
		c.mu.Unlock()
		return errors.New("host power requires the sole active root job")
	}
	if strings.TrimSpace(goalID) == "" || strings.TrimSpace(title) == "" || recheck == nil {
		c.mu.Unlock()
		return errors.New("host power requires a goal, trusted title, and verification recheck")
	}
	if c.state.Status == "armed" || c.state.Status == "countdown" || c.state.Status == "executing" {
		c.mu.Unlock()
		return errors.New("host power is already authorized; cancel before arming again")
	}
	cleanup := c.invalidateLocked()
	c.state = Snapshot{Status: "armed", JobID: jobID, GoalID: goalID, Goal: title}
	c.recheck = recheck
	c.mu.Unlock()
	cleanup()
	return nil
}

// Finish removes an active root. Only the approved job's explicit successful
// finish, plus a fresh recheck, may create a countdown. The caller must not pass
// successful=true merely because an ordinary agent turn ended.
func (c *Controller) Finish(jobID string, successful bool) {
	c.mu.Lock()
	if c.closed || !c.active[jobID] {
		c.mu.Unlock()
		return
	}
	delete(c.active, jobID)
	if c.state.Status != "armed" || c.state.JobID != jobID {
		c.mu.Unlock()
		return
	}
	if !successful || len(c.active) != 0 {
		cleanup := c.invalidateLocked()
		c.state.Status, c.state.Error = "blocked", "approved work failed or another root is active"
		c.mu.Unlock()
		cleanup()
		return
	}
	gen, check := c.generation, c.recheck
	c.mu.Unlock()
	err := check()
	now := c.opts.Now()
	c.mu.Lock()
	if c.closed || c.generation != gen || c.state.Status != "armed" {
		c.mu.Unlock()
		return
	}
	if err != nil {
		cleanup := c.invalidateLocked()
		c.state.Status, c.state.Error = "blocked", err.Error()
		c.mu.Unlock()
		cleanup()
		return
	}
	c.state.Status = "countdown"
	c.state.Deadline = now.Add(c.opts.Delay).UnixMilli()
	c.mu.Unlock()
	stop := c.opts.AfterFunc(c.opts.Delay, func() { c.deadline(gen) })
	c.mu.Lock()
	if !c.closed && c.generation == gen && c.state.Status == "countdown" {
		c.stop = stop
		stop = nil
	}
	c.mu.Unlock()
	if stop != nil {
		stop()
	}
}

func (c *Controller) deadline(gen uint64) {
	now := c.opts.Now()
	c.mu.Lock()
	if c.closed || c.generation != gen || c.state.Status != "countdown" {
		c.mu.Unlock()
		return
	}
	if len(c.active) != 0 || now.UnixMilli() < c.state.Deadline {
		cleanup := c.invalidateLocked()
		c.state.Status, c.state.Error = "blocked", "deadline arrived early or roots are active"
		c.mu.Unlock()
		cleanup()
		return
	}
	check := c.recheck
	c.mu.Unlock()
	err := check()
	c.mu.Lock()
	if c.closed || c.generation != gen || c.state.Status != "countdown" {
		c.mu.Unlock()
		return
	}
	if err != nil || len(c.active) != 0 {
		cleanup := c.invalidateLocked()
		c.state.Status, c.state.Error = "blocked", "work is no longer eligible"
		if err != nil {
			c.state.Error = err.Error()
		}
		c.mu.Unlock()
		cleanup()
		return
	}
	// Claim and consume once before crossing into the executor. Repeated timer
	// callbacks cannot execute again, even while this call is in progress.
	c.recheck, c.stop = nil, nil
	c.state.Status, c.state.Deadline = "executing", 0
	c.mu.Unlock()
	err = c.opts.Execute(context.Background())
	c.mu.Lock()
	if c.generation == gen && c.state.Status == "executing" {
		c.state.Status = "executed"
		if err != nil {
			c.state.Status, c.state.Error = "failed", err.Error()
		}
	}
	c.mu.Unlock()
}

func (c *Controller) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Cancel revokes authorization only before claim. After claim it returns the
// executing or terminal snapshot unchanged; it cannot confirm cancellation.
func (c *Controller) Cancel() Snapshot {
	c.mu.Lock()
	if c.executionClaimedLocked() {
		s := c.state
		c.mu.Unlock()
		return s
	}
	cleanup := c.invalidateLocked()
	c.state.Status, c.state.Error = "cancelled", ""
	s := c.state
	c.mu.Unlock()
	cleanup()
	return s
}

// Close permanently revokes unclaimed authorization, without starting execution.
// An already claimed executor continues and its eventual result remains visible.
func (c *Controller) Close() {
	c.mu.Lock()
	c.closed = true
	if c.executionClaimedLocked() {
		c.mu.Unlock()
		return
	}
	cleanup := c.invalidateLocked()
	c.state.Status, c.state.Error = "cancelled", ""
	c.mu.Unlock()
	cleanup()
}
