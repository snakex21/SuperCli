package agent

import (
	"time"

	"supercli/internal/llm"
	"supercli/internal/system/stats"
)

// statsStartStep opens a new telemetry turn for a step (1-based)
// and resets the wall-phase accumulator. Nil recorder = no-op, so
// loops built without stats (tests, child workers) pay nothing.
func (l *Loop) statsStartStep(step int) {
	if l.stats == nil {
		return
	}
	l.stepPhaseWall = 0
	l.stepAuxWall = 0
	l.statsStepOpen = true
	l.stats.StartStep(step)
	l.flushContextAdditions()
	l.flushWorkerContextAdditions()
	// Helper inference that ran before this turn opened (the
	// navigator route call) is charged to the turn it delayed —
	// counted only, never added to the disjoint phase pipeline,
	// which measures time INSIDE the step.
	if l.pendingAuxCalls > 0 {
		l.stats.RecordAux(l.pendingAuxCalls, l.pendingAuxWall)
		l.pendingAuxCalls = 0
		l.pendingAuxWall = 0
	}
}

func (l *Loop) recordContextAddition(msg llm.Message) {
	if l.stats == nil {
		return
	}
	epoch := l.conversationEpoch.Load()
	if l.pendingContextEpoch != epoch {
		l.pendingContextUsage = llm.ContextUsage{}
	}
	l.pendingContextEpoch = epoch
	l.pendingContextUsage.Add(llm.ContextAddition(msg))
	if l.statsStepOpen {
		l.flushContextAdditions()
	}
}

func (l *Loop) flushContextAdditions() {
	pending := l.pendingContextUsage
	l.pendingContextUsage = llm.ContextUsage{}
	if l.pendingContextEpoch != l.conversationEpoch.Load() || len(pending.Sources) == 0 {
		return
	}
	if rec, ok := l.stats.(interface{ RecordContextUsage(llm.ContextUsage) }); ok {
		rec.RecordContextUsage(pending)
	}
}

// Worker additions are counted once on the conversation owner. Retransmission
// is counted separately by the metered provider, after context projection.
func (l *Loop) recordWorkerContextAddition(msg llm.Message) {
	if l.stats == nil || msg.Role != llm.RoleUser || msg.Name != llm.WorkerNotificationName {
		return
	}
	epoch := l.conversationEpoch.Load()
	if l.pendingWorkerContextEpoch != epoch {
		l.pendingWorkerContext = stats.WorkerContext{}
	}
	l.pendingWorkerContextEpoch = epoch
	l.pendingWorkerContext.AddedMessages++
	l.pendingWorkerContext.AddedBytes += int64(len(msg.Content))
	if l.statsStepOpen {
		l.flushWorkerContextAdditions()
	}
}

func (l *Loop) flushWorkerContextAdditions() {
	pending := l.pendingWorkerContext
	l.pendingWorkerContext = stats.WorkerContext{}
	if l.pendingWorkerContextEpoch != l.conversationEpoch.Load() || pending.Empty() {
		return
	}
	if recorder, ok := l.stats.(interface{ RecordWorkerContext(stats.WorkerContext) }); ok {
		recorder.RecordWorkerContext(pending)
	}
}

// recordPhase forwards one phase measurement to the recorder.
// Safe from any goroutine (the recorder locks internally) and
// with a nil recorder.
func (l *Loop) recordPhase(name string, d time.Duration) {
	if l.stats == nil {
		return
	}
	l.stats.RecordPhase(name, d)
}

// recordWallPhase records a phase that is part of the step's
// DISJOINT wall-clock pipeline (context_prepare, request_encode,
// backend_wait, stream_total, tool_execution) and adds it to the
// accumulator statsEndStep subtracts from the step total. Must be
// called from the run goroutine only.
func (l *Loop) recordWallPhase(name string, d time.Duration) {
	if l.stats == nil {
		return
	}
	l.stepPhaseWall += d
	l.stats.RecordPhase(name, d)
}

// recordAuxWall records the wall time of a model-powered aux
// operation (draft, compact summary, reflection, navigator) as its
// own "model:<purpose>" phase and adds it to both accumulators, so
// the surrounding CLI phases (context_prepare, the
// next_turn_prepare remainder) exclude it. The SAME duration also
// feeds the turn's aux call counter (stats.Turn.AuxCalls/AuxUs) —
// one measurement, two views, so the counter can never disagree
// with the phase line. Run goroutine only.
func (l *Loop) recordAuxWall(purpose string, d time.Duration) {
	if l.stats == nil {
		return
	}
	if !l.statsStepOpen {
		// Between steps (navigator): buffer for the next turn.
		l.pendingAuxCalls++
		l.pendingAuxWall += d
		return
	}
	l.stepAuxWall += d
	l.recordWallPhase("model:"+purpose, d)
	l.stats.RecordAux(1, d)
}

// statsEndStep attributes the unmeasured remainder of the step to
// next_turn_prepare (history append, eviction, draft accounting,
// reflection, event plumbing) and closes the telemetry turn.
// Called on every step exit path.
func (l *Loop) statsEndStep(stepStart time.Time) {
	if l.stats == nil {
		return
	}
	rest := time.Since(stepStart) - l.stepPhaseWall
	if rest < 0 {
		rest = 0
	}
	l.stats.RecordPhase(stats.PhaseNextTurnPrepare, rest)
	l.stats.EndStep()
	l.statsStepOpen = false
}
