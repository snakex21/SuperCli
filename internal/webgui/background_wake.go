package webgui

import (
	"context"
	"errors"
	"log"
	"sync"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/tools"
)

// Background agents in the web GUI. Every chat run builds a fresh agent.Loop,
// and task{async:true} workers outlive that run. Before 1.0.35 the run
// detached its event sink on exit, so a worker that kept going after the user
// sent another message (or stopped the turn) became invisible — its card
// froze — and its finished report sat in storage with no turn to read it.
//
// sessionBackground keeps one long-lived pump per conversation. A finished
// run hands its loop over: the loop's external sink is re-pointed to the pump
// (worker progress/completions are published to every window through the sync
// hub) and its WakeScheduler is the conversation's scheduler, which starts an
// automatic turn (Loop.RunWake on a fresh loop) for unseen reports.

const wakeRunTitle = "[automatic wake-up: background agents finished]"

type wakeRunKey struct{}

func isWakeRun(ctx context.Context) bool {
	wake, _ := ctx.Value(wakeRunKey{}).(bool)
	return wake
}

type sessionBackground struct {
	e      *Engine
	sid    string
	home   string
	sched  *agent.WakeScheduler
	events chan agent.Event
	// ask is the conversation's durable question route (background_questions.go).
	ask chan tools.AskRequest
	// stop ends the question goroutines when the engine closes.
	stop chan struct{}

	mu    sync.Mutex
	loops map[*agent.Loop]struct{}
	last  int // last published running-agent count
}

func (e *Engine) wakeConfig(home string) agent.WakeConfig {
	tc := e.tomlConfigAt(home)
	cfg := agent.WakeConfigFrom(tc.BackgroundWakeSettings())
	cfg.Delegate = tc.WakeDelegate()
	return cfg
}

// backgroundFor returns (creating on first use) the conversation's pump.
func (e *Engine) backgroundFor(sid, home string) *sessionBackground {
	e.bgMu.Lock()
	defer e.bgMu.Unlock()
	if e.bgClosed {
		return nil
	}
	if bg := e.bg[sid]; bg != nil {
		if home != "" {
			bg.home = home
			// Settings may have changed since the conversation's first run.
			if cfg := e.wakeConfig(home); cfg != bg.sched.Config() {
				bg.sched.SetConfig(cfg)
			}
		}
		return bg
	}
	if e.bg == nil {
		e.bg = make(map[string]*sessionBackground)
	}
	bg := &sessionBackground{e: e, sid: sid, home: home, events: make(chan agent.Event, 256), ask: make(chan tools.AskRequest, backgroundAskBuffer), stop: make(chan struct{}), loops: map[*agent.Loop]struct{}{}}
	bg.sched = agent.NewWakeScheduler(e.wakeConfig(home), bg.state, bg.startWake, bg.held)
	bg.sched.SetReleaseHook(func() { e.syncHub.backgroundWakeHeld(sid, 0, 0) })
	e.bg[sid] = bg
	go bg.pump()
	go bg.askPump()
	return bg
}

// attach makes loop part of the conversation: shared scheduler always; after
// its run ends (detached=true) it also publishes through the pump.
func (bg *sessionBackground) attach(loop *agent.Loop, detached bool) {
	if bg == nil || loop == nil {
		return
	}
	loop.SetWakeScheduler(bg.sched)
	bg.mu.Lock()
	bg.loops[loop] = struct{}{}
	bg.mu.Unlock()
	if !detached {
		return
	}
	loop.SetExternalSink(bg.events)
	bg.publishCount()
	bg.sched.Signal()
}

// acknowledge: a new run loads the persisted conversation, which contains
// every report the detached loops have saved so far.
func (bg *sessionBackground) acknowledge() {
	if bg == nil {
		return
	}
	bg.mu.Lock()
	for loop := range bg.loops {
		loop.AcknowledgeBackgroundReports()
	}
	bg.mu.Unlock()
	bg.prune()
}

func (bg *sessionBackground) prune() {
	bg.mu.Lock()
	for loop := range bg.loops {
		state := loop.BackgroundWakeState()
		if state.Active == 0 && state.Unseen == 0 && !state.Busy {
			delete(bg.loops, loop)
		}
	}
	bg.mu.Unlock()
}

func (bg *sessionBackground) state() agent.WakeState {
	var total agent.WakeState
	bg.mu.Lock()
	for loop := range bg.loops {
		s := loop.BackgroundWakeState()
		total.Unseen += s.Unseen
		total.Active += s.Active
	}
	bg.mu.Unlock()
	total.Busy = bg.e.sessionRunActive(bg.sid)
	return total
}

func (bg *sessionBackground) running() int {
	return bg.state().Active
}

func (bg *sessionBackground) publishCount() {
	n := bg.running()
	bg.mu.Lock()
	changed := n != bg.last
	bg.last = n
	bg.mu.Unlock()
	if changed {
		bg.e.syncHub.backgroundAgents(bg.sid, n)
	}
}

// held: automatic wake-ups paused at the cap. The view shows a persistent
// banner with a Resume button (1.0.38; before, only a toast that was easy to
// miss, so the conversation looked idle while reports piled up).
func (bg *sessionBackground) held(unseen int) {
	bg.e.syncHub.backgroundWakeHeld(bg.sid, unseen, bg.sched.Config().MaxConsecutive)
}

// ResumeBackgroundWake ends a hold at the automatic wake-up cap: the budget
// is restored and the held reports start an automatic turn right away.
// ok=false: the conversation has no background state in this process.
func (e *Engine) ResumeBackgroundWake(sid string) bool {
	e.bgMu.Lock()
	bg := e.bg[sid]
	e.bgMu.Unlock()
	if bg == nil {
		return false
	}
	bg.sched.Resume()
	return true
}

func (bg *sessionBackground) pump() {
	for ev := range bg.events {
		switch ev.(type) {
		case agent.BackgroundAgentsEvent:
			bg.publishCount()
			continue
		case agent.WorkerNotificationEvent, agent.WorkerProgressEvent:
		default:
			continue // detached loops have no live turn to render
		}
		if w, keep := toWireEvent(ev); keep {
			bg.e.syncHub.background(bg.sid, w)
		}
	}
}

func (bg *sessionBackground) startWake(req agent.WakeRequest) bool {
	bg.e.bgMu.Lock()
	home := bg.home // written by backgroundFor under bgMu
	bg.e.bgMu.Unlock()
	go bg.e.runBackgroundWake(bg.sid, home, req)
	return true
}

// runBackgroundWake runs one automatic turn on a fresh loop, exactly like a
// chat run (sync hub, persistence, telemetry) but without a user message.
func (e *Engine) runBackgroundWake(sid, home string, req agent.WakeRequest) {
	if home == "" || !sameSessionWorkspace(home, e.Home()) {
		// The user switched projects; the reports wait in the conversation.
		return
	}
	ctx, cancel, finish := e.beginChatRun(context.Background())
	defer finish()
	defer cancel()
	ctx = context.WithValue(ctx, wakeRunKey{}, true)
	err := e.runStreamWithImages(ctx, wakeRunTitle, sid, "", nil, func(wireEvent) {})
	if err != nil && !errors.Is(err, errSessionBusy) && !errors.Is(err, agent.ErrNothingToWake) && !errors.Is(err, context.Canceled) {
		log.Printf("background wake-up: session=%q: %v", sid, err)
	}
}

// BackgroundAgentCounts reports running background workers per conversation.
func (e *Engine) BackgroundAgentCounts() map[string]int {
	e.bgMu.Lock()
	list := make([]*sessionBackground, 0, len(e.bg))
	for _, bg := range e.bg {
		list = append(list, bg)
	}
	e.bgMu.Unlock()
	out := map[string]int{}
	for _, bg := range list {
		if n := bg.running(); n > 0 {
			out[bg.sid] = n
		}
	}
	return out
}

func (e *Engine) closeBackground() {
	e.bgMu.Lock()
	e.bgClosed = true
	list := e.bg
	e.bg = nil
	e.bgMu.Unlock()
	for _, bg := range list {
		bg.sched.Stop()
		close(bg.stop)
	}
}

// wakeActivitySeq is the first sequence the automatic turn will write; the
// browser shows saved history before it and the live turn after it.
func wakeActivitySeq(ctx context.Context, latest func(context.Context, string, string) (int, error), sid string) int {
	seq := 0
	for _, role := range []llm.Role{llm.RoleUser, llm.RoleAssistant, llm.RoleTool, llm.RoleSystem} {
		if n, err := latest(ctx, sid, string(role)); err == nil && n > seq {
			seq = n
		}
	}
	return seq + 1
}

// savedWorkerRows reconciles and lists a conversation's saved workers that
// are not live in this process.
func (e *Engine) savedWorkerRows(ctx context.Context, sid string, seen map[string]bool) []webWorker {
	if e.workers == nil {
		return nil
	}
	if _, err := e.sessionStore(); err != nil { // attaches worker persistence
		return nil
	}
	if _, err := e.workers.ReconcileSavedWorkers(ctx, sid); err != nil {
		log.Printf("worker reconcile: session=%q: %v", sid, err)
	}
	store := e.workers.Store()
	if store == nil {
		return nil
	}
	recs, err := store.ListWorkers(ctx, sid)
	if err != nil {
		return nil
	}
	var rows []webWorker
	for _, rec := range recs {
		if seen[rec.ID] {
			continue
		}
		rows = append(rows, webWorker{ID: rec.ID, Agent: rec.Agent, Description: rec.Description, Model: rec.Model,
			Status: rec.Status, LastError: rec.LastError, Run: rec.Runs, UpdatedAt: rec.UpdatedAt.UnixMilli(), Resumable: true, Session: sid})
	}
	return rows
}

// reconcileWorkersIntoRun: saved workers whose runner disappeared (restart,
// crash) become "interrupted" and the coordinator is told so at the start of
// its next turn, with the way to resume them.
func (e *Engine) reconcileWorkersIntoRun(ctx context.Context, loop *agent.Loop, sid string) {
	if e.workers == nil || loop == nil || sid == "" {
		return
	}
	changed, err := e.workers.ReconcileSavedWorkers(ctx, sid)
	if err != nil || len(changed) == 0 {
		return
	}
	for _, rec := range changed {
		loop.DeliverWorkerReport(ctx, interruptedWorkerNotice(rec))
	}
}

func interruptedWorkerNotice(rec agent.WorkerRecord) string {
	return "<task-notification>\n<task-id>" + rec.ID + "</task-id>\n<agent>" + rec.Agent + "</agent>\n<status>" + agent.WorkerStatusInterrupted +
		"</status>\n<summary>" + rec.ID + " was interrupted: " + rec.LastError + "</summary>\n<continuation>Its saved conversation is kept; send_message to " + rec.ID +
		" resumes it with full context. Do not restart it as a new task.</continuation>\n</task-notification>"
}
