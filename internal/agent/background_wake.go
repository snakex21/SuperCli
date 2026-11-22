package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"supercli/internal/llm"
)

// Background wake-up: a worker started with task{async:true} can finish after
// its parent turn has already ended (Stop/cancel, an error, the max-steps
// guard, a GUI per-run loop, or a worker left over from an earlier turn). Its
// report is saved to the conversation, but before this file nothing started a
// model turn for it, so the coordinator never told the user anything.
//
// The conversation owner counts reports appended after the model's last
// request ("unseen"). Whenever such reports exist while the conversation is
// idle, a WakeScheduler (one per conversation, owned by the host) debounces
// them, applies the wake policy and the consecutive-wake cap, and asks the
// host to start an automatic turn through Loop.RunWake. RunWake appends no
// user message: the reports are already in history as worker notifications,
// and a request-only hint tells the model this turn was not user input.

// WakeMode selects when an idle conversation is woken for finished workers.
type WakeMode string

const (
	// WakeEach wakes after every completion (after the debounce window).
	WakeEach WakeMode = "each"
	// WakeAll waits until no background worker of the conversation is running.
	WakeAll WakeMode = "all"
	// WakeOff never starts automatic turns; reports wait for the next message.
	WakeOff WakeMode = "off"
)

const (
	DefaultWakeDebounce = 1500 * time.Millisecond
	// DefaultWakeMaxConsecutive caps consecutive automatic turns that made no
	// progress (1.0.38). A turn that starts or resumes a worker, changes a
	// file, runs a command or updates the goal resets the count, so a
	// coordinator that keeps a worker pool full is never held; one that only
	// reads reports and answers stops after this many idle wake-ups.
	// Before 1.0.38 every automatic turn counted and the cap was 10: with
	// 30 parallel workers it was reached in ~15 minutes while the coordinator
	// was still refilling freed slots, and the remaining reports waited for
	// the user's next message.
	DefaultWakeMaxConsecutive = 20
)

// ErrNothingToWake is returned by RunWake when no unseen worker report exists.
var ErrNothingToWake = errors.New("no unseen background results")

// ParseWakeMode accepts the config spellings; unknown/empty means WakeEach.
func ParseWakeMode(s string) WakeMode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "all", "all_done", "all-done", "when_all_done":
		return WakeAll
	case "off", "false", "no", "none", "never", "0":
		return WakeOff
	default:
		return WakeEach
	}
}

// WakeConfig is the host-resolved policy. Zero values mean defaults.
type WakeConfig struct {
	Mode           WakeMode
	Debounce       time.Duration
	MaxConsecutive int
}

func (c WakeConfig) normalized() WakeConfig {
	if c.Mode == "" {
		c.Mode = WakeEach
	}
	if c.Debounce <= 0 {
		c.Debounce = DefaultWakeDebounce
	}
	if c.MaxConsecutive <= 0 {
		c.MaxConsecutive = DefaultWakeMaxConsecutive
	}
	return c
}

// WakeState is what the scheduler needs to decide; the host aggregates it.
type WakeState struct {
	Unseen int  // finished-worker reports the model has not been asked about
	Active int  // background workers still running for this conversation
	Busy   bool // a turn (user or automatic) currently owns the conversation
}

// WakeRequest is handed to the host's start callback.
type WakeRequest struct {
	Unseen      int
	Consecutive int // automatic turns without progress since the last user input or progress
}

// WakeScheduler is safe for concurrent use. Signal is cheap and non-blocking;
// all decisions happen on the debounce timer's goroutine.
type WakeScheduler struct {
	cfg   WakeConfig
	probe func() WakeState
	start func(WakeRequest) bool
	held  func(unseen int)

	mu          sync.Mutex
	timer       *time.Timer
	consecutive int
	heldNoticed bool
	heldUnseen  int // >0 while reports are held at the cap
	stopped     bool
	afterFunc   func(time.Duration, func()) *time.Timer
	onRelease   func() // host hook: the hold ended (user turn, resume, progress)
}

// NewWakeScheduler builds a scheduler. probe reports the conversation state,
// start asks the host to begin an automatic turn (true = started), held is
// called once per hold period when the consecutive-wake cap is reached.
func NewWakeScheduler(cfg WakeConfig, probe func() WakeState, start func(WakeRequest) bool, held func(int)) *WakeScheduler {
	return &WakeScheduler{cfg: cfg.normalized(), probe: probe, start: start, held: held, afterFunc: time.AfterFunc}
}

// Config returns the active policy.
func (s *WakeScheduler) Config() WakeConfig {
	if s == nil {
		return WakeConfig{}.normalized()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// SetConfig replaces the policy (settings changed at runtime).
func (s *WakeScheduler) SetConfig(cfg WakeConfig) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.cfg = cfg.normalized()
	s.mu.Unlock()
	s.Signal()
}

// Signal (re)arms the debounce window. Several completions close together
// therefore produce one automatic turn that sees all of their reports.
func (s *WakeScheduler) Signal() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || s.cfg.Mode == WakeOff {
		return
	}
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = s.afterFunc(s.cfg.Debounce, s.fire)
}

// UserTurn records real user input: the consecutive-wake budget is restored.
// Held reports need no action: they are already in the conversation history
// that the user's turn sends to the model.
func (s *WakeScheduler) UserTurn() {
	if s == nil {
		return
	}
	s.mu.Lock()
	release := s.resetLocked()
	s.mu.Unlock()
	if release != nil {
		release()
	}
}

// resetLocked restores the budget and ends a hold; it returns the host's
// release hook when a hold was active. Caller holds mu.
func (s *WakeScheduler) resetLocked() func() {
	wasHeld := s.heldUnseen > 0 || s.heldNoticed
	s.consecutive = 0
	s.heldNoticed = false
	s.heldUnseen = 0
	if wasHeld {
		return s.onRelease
	}
	return nil
}

// WakeStarted counts one automatic turn against the consecutive cap. It is
// counted as "no progress" until WakeFinished reports otherwise.
func (s *WakeScheduler) WakeStarted() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.consecutive++
	s.mu.Unlock()
}

// WakeFinished closes an automatic turn. A turn that made progress (started
// or resumed a worker, changed files, ran a command, updated the goal)
// restores the whole budget: only consecutive idle wake-ups count.
func (s *WakeScheduler) WakeFinished(progress bool) {
	if s == nil || !progress {
		return
	}
	s.mu.Lock()
	release := s.resetLocked()
	s.mu.Unlock()
	if release != nil {
		release()
	}
}

// Resume ends a hold at the cap on the user's request (GUI "Resume" button)
// without a chat message: the budget is restored and held reports wake the
// conversation right away.
func (s *WakeScheduler) Resume() {
	if s == nil {
		return
	}
	s.mu.Lock()
	release := s.resetLocked()
	s.mu.Unlock()
	if release != nil {
		release()
	}
	s.Signal()
}

// Held reports the number of reports waiting at the cap (0 = not held).
func (s *WakeScheduler) Held() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.heldUnseen
}

// SetReleaseHook registers a callback for the end of a hold (host UI state).
func (s *WakeScheduler) SetReleaseHook(fn func()) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.onRelease = fn
	s.mu.Unlock()
}

// Consecutive reports automatic turns without progress since the last user
// input (or the last automatic turn that made progress).
func (s *WakeScheduler) Consecutive() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.consecutive
}

// Stop cancels any pending wake; later Signals are ignored.
func (s *WakeScheduler) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.stopped = true
	if s.timer != nil {
		s.timer.Stop()
	}
	s.mu.Unlock()
}

func (s *WakeScheduler) fire() {
	s.mu.Lock()
	if s.stopped || s.cfg.Mode == WakeOff || s.probe == nil || s.start == nil {
		s.mu.Unlock()
		return
	}
	cfg, consecutive := s.cfg, s.consecutive
	s.mu.Unlock()
	state := s.probe()
	// Busy: the running turn sees the reports at its next tool boundary or
	// final answer; if it does not, the conversation's release signals again.
	if state.Unseen <= 0 || state.Busy {
		return
	}
	if cfg.Mode == WakeAll && state.Active > 0 {
		return // the last finishing worker signals again
	}
	if consecutive >= cfg.MaxConsecutive {
		s.mu.Lock()
		// Re-notify when more reports arrived so the host's banner count is
		// current; the first notice also starts the hold.
		notify := !s.heldNoticed || s.heldUnseen != state.Unseen
		s.heldNoticed = true
		s.heldUnseen = state.Unseen
		s.mu.Unlock()
		if notify && s.held != nil {
			s.held(state.Unseen)
		}
		return
	}
	s.start(WakeRequest{Unseen: state.Unseen, Consecutive: consecutive})
}

// --- Loop side -----------------------------------------------------------

// BackgroundWakeEvent asks the host (TUI) to start an automatic turn with
// Loop.RunWake. It is emitted on the external sink by the default scheduler
// that SetBackgroundWake installs.
type BackgroundWakeEvent struct {
	Unseen      int
	Consecutive int
}

func (BackgroundWakeEvent) event() {}

// BackgroundWakeHeldEvent: the cap of consecutive automatic turns without
// progress was reached and Unseen reports wait for the next user message (or
// an explicit resume).
type BackgroundWakeHeldEvent struct {
	Unseen int
	Limit  int
	// First is true for the notice that starts a hold; later ones only
	// update the waiting count.
	First bool
}

func (BackgroundWakeHeldEvent) event() {}

// BackgroundAgentsEvent reports a change in the number of running background
// workers or unseen reports, for status indicators. Best-effort.
type BackgroundAgentsEvent struct {
	Active int
	Unseen int
}

func (BackgroundAgentsEvent) event() {}

// WakeHeldNotice is the user-facing text when the consecutive cap holds
// reports until the next message.
func WakeHeldNotice(unseen, limit int) string {
	return fmt.Sprintf("Automatic wake-up paused: %d finished agent report(s) wait for your next message (%d automatic turns in a row made no progress; limit background_wake_max_auto).", unseen, limit)
}

// SetWakeScheduler attaches a host-owned scheduler. Several loops of the same
// conversation (GUI per-run loops) may share one scheduler.
func (l *Loop) SetWakeScheduler(s *WakeScheduler) {
	l.wakeMu.Lock()
	l.wake = s
	l.wakeMu.Unlock()
	if s != nil && l.unseenNotices.Load() > 0 && !l.sessionBusy.Load() {
		s.Signal()
	}
}

// WakeScheduler returns the attached scheduler, if any.
func (l *Loop) WakeScheduler() *WakeScheduler {
	l.wakeMu.Lock()
	defer l.wakeMu.Unlock()
	return l.wake
}

// SetBackgroundWake installs the standard single-loop scheduler (TUI/CLI):
// a due wake is announced as BackgroundWakeEvent on the external sink and a
// held cap as NoticeEvent. Mode off still installs it so the indicator events
// keep flowing, but it never wakes.
func (l *Loop) SetBackgroundWake(cfg WakeConfig) *WakeScheduler {
	if existing := l.WakeScheduler(); existing != nil {
		existing.SetConfig(cfg)
		return existing
	}
	var s *WakeScheduler
	var holding atomic.Bool
	s = NewWakeScheduler(cfg, l.BackgroundWakeState, func(req WakeRequest) bool {
		return l.Emit(BackgroundWakeEvent{Unseen: req.Unseen, Consecutive: req.Consecutive})
	}, func(unseen int) {
		l.Emit(BackgroundWakeHeldEvent{Unseen: unseen, Limit: s.Config().MaxConsecutive, First: !holding.Swap(true)})
	})
	s.SetReleaseHook(func() { holding.Store(false) })
	l.SetWakeScheduler(s)
	return s
}

// BackgroundWakeState reports this loop's own wake inputs.
func (l *Loop) BackgroundWakeState() WakeState {
	return WakeState{Unseen: int(l.unseenNotices.Load()), Active: int(l.backgroundActive.Load()), Busy: l.sessionBusy.Load()}
}

// UnseenBackgroundReports is the number of worker reports appended after the
// model's latest request.
func (l *Loop) UnseenBackgroundReports() int { return int(l.unseenNotices.Load()) }

// ActiveBackgroundWorkers is the number of task{async:true} workers started by
// this loop that have not delivered their report yet.
func (l *Loop) ActiveBackgroundWorkers() int { return int(l.backgroundActive.Load()) }

// AcknowledgeBackgroundReports marks this loop's reports as handed over. The
// GUI calls it on an orphaned per-run loop when a newer loop has loaded the
// persisted conversation (which contains those reports).
func (l *Loop) AcknowledgeBackgroundReports() {
	if l.unseenNotices.Swap(0) != 0 {
		l.emitBackgroundAgents()
	}
}

func (l *Loop) signalWake() {
	if s := l.WakeScheduler(); s != nil {
		s.Signal()
	}
}

func (l *Loop) emitBackgroundAgents() {
	l.Emit(BackgroundAgentsEvent{Active: int(l.backgroundActive.Load()), Unseen: int(l.unseenNotices.Load())})
}

// backgroundWorkerStarted/Finished bracket a detached worker's lifetime.
func (l *Loop) backgroundWorkerStarted() {
	l.backgroundActive.Add(1)
	l.emitBackgroundAgents()
}

func (l *Loop) backgroundWorkerFinished() {
	l.backgroundActive.Add(-1)
	l.emitBackgroundAgents()
	// WakeAll waits for this moment; WakeEach may also be waiting for an idle
	// conversation that already holds the report.
	if l.unseenNotices.Load() > 0 && !l.sessionBusy.Load() {
		l.signalWake()
	}
}

// wakeTurnHint is request-only (never persisted). It marks the turn as an
// automated event so the model neither treats reports as user words nor
// restarts finished tasks.
const wakeTurnHint = "[automatic wake-up — not user input] Background agent(s) you started have finished; their reports are the task-notification messages above. " +
	"Keep pursuing the user's stated goal and standing instructions (below and in the goal state) without waiting for a new message: " +
	"if more steps remain, start them now (e.g. refill freed worker slots when the user asked to keep agents busy); " +
	"when the goal is reached or blocked, tell the user the outcome concisely. Do not restart finished tasks, " +
	"and do not resume a worker that was canceled or stopped (\"context canceled\", stopped by request): report it and let the user decide. " +
	"A report shown only as a preview can be read in full with read_output using its handle."

// wakeGoalBudget bounds the user's recent instructions repeated in a wake-up
// hint (request-only; the messages themselves stay in history unchanged).
const (
	wakeGoalMessages = 3
	wakeGoalRunes    = 500
)

// wakeUserGoal returns the user's latest instructions, oldest first, as a
// compact block for an automatic turn. Long orchestrations push the user's
// request far up the history (hundreds of worker reports later); repeating
// it here keeps the coordinator on the goal across wake-ups so the user does
// not have to restate it.
func wakeUserGoal(msgs []llm.Message) string {
	var picked []string
	for i := len(msgs) - 1; i >= 0 && len(picked) < wakeGoalMessages; i-- {
		if !llm.IsConversationUserTurn(msgs[i]) {
			continue
		}
		text := strings.Join(strings.Fields(msgs[i].Content), " ")
		if text == "" {
			continue
		}
		if r := []rune(text); len(r) > wakeGoalRunes {
			text = string(r[:wakeGoalRunes]) + "…"
		}
		picked = append(picked, text)
	}
	if len(picked) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nThe user's most recent instructions (oldest first; still in force unless already done or superseded):")
	for i := len(picked) - 1; i >= 0; i-- {
		b.WriteString("\n- «")
		b.WriteString(picked[i])
		b.WriteString("»")
	}
	return b.String()
}

// RunWake starts an automatic turn for unseen background reports without
// appending a user message. requireUnseen=false lets a host that loaded the
// reports from storage into a fresh loop (GUI) run regardless of this loop's
// own counter.
func (l *Loop) RunWake(ctx context.Context, requireUnseen bool) (<-chan Event, error) {
	if requireUnseen && l.unseenNotices.Load() <= 0 && !l.backgroundPending.Load() {
		return nil, ErrNothingToWake
	}
	if len(l.Messages) == 0 {
		return nil, ErrNothingToWake
	}
	return l.runTurn(ctx, wakeRunPrompt, true)
}

// wakeRunPrompt is a non-empty placeholder satisfying Run's contract. It is
// never appended to history or sent to the model (see runWakePrelude).
const wakeRunPrompt = "[automatic wake-up: background agents finished]"

// prepareWakeRoute replaces prompt routing for an automatic turn: the
// coordinator handles reports with no navigator call and no one-shot addons.
func (l *Loop) prepareWakeRoute() {
	l.implementationHintForRun = wakeTurnHint + wakeUserGoal(l.Messages)
	l.statusOnlyForRun = false
	l.screenshotForRun, l.headlessForRun = false, false
	l.downloadForRun, l.downloadPageForRun = false, false
	l.webForRun, l.webURLForRun = false, false
	l.route = RouteCoordinator
}

// lastIsWorkerNotification reports whether history already ends with a
// worker report (the shape RunWake needs: the final message is user-role).
func lastIsWorkerNotification(msgs []llm.Message) bool {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleSystem {
			continue
		}
		return msgs[i].Role == llm.RoleUser && msgs[i].Name == llm.WorkerNotificationName
	}
	return false
}

// WakeConfigFrom maps config.toml values (zero = default) to a WakeConfig.
func WakeConfigFrom(mode string, debounceMS, maxAuto int) WakeConfig {
	return WakeConfig{Mode: ParseWakeMode(mode), Debounce: time.Duration(debounceMS) * time.Millisecond, MaxConsecutive: maxAuto}.normalized()
}

// DeliverWorkerReport hands the conversation a finished background report
// produced outside the task tool (hosts, tests). It follows the same rules as
// a task worker's delivery: safe history boundary, persistence, wake-up.
func (l *Loop) DeliverWorkerReport(ctx context.Context, content string) {
	l.backgroundDelivery(ctx)(ctx, content)
}

// backgroundHandoff: a final answer no longer waits for this turn's detached
// workers when a host scheduler will wake the conversation for their reports.
// Without one (batch mode, worker loops, mode off) the turn keeps the
// original blocking wait so no report can be stranded.
func (l *Loop) backgroundHandoff() bool {
	s := l.WakeScheduler() // workers never get one (depth 1)
	return s != nil && s.Config().Mode != WakeOff
}
