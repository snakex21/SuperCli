package webgui

import (
	"context"

	"supercli/internal/llm"
)

// Helper inference that runs INSIDE an agent turn (navigator, draft,
// reflection, auto-compaction) is charged to that turn and surfaces as
// the turn summary's aux counters. The GUI also makes model calls that
// belong to no turn at all: session titles, run summaries, folder and
// document index summaries, image captions, vision transcription.
//
// Those calls have no turn to be attributed to, yet the user waits for
// them and pays for them. They are counted here, once per Engine (that
// is, per app run), from the durations llm.Metered already measured —
// no second clock, no extra request.

// countOffTurnCalls attaches the off-turn counter to ctx. Every
// provider call made under the returned context is counted, including
// calls made deeper in the call tree, because llm.WithCallSink appends
// to the context's sink list instead of replacing it.
func (e *Engine) countOffTurnCalls(ctx context.Context) context.Context {
	if e == nil {
		return ctx
	}
	return llm.WithCallSink(ctx, e.offTurnSink())
}

// offTurnSink books one off-turn call. The duration is the one
// llm.Metered already measured — never a second measurement.
func (e *Engine) offTurnSink() llm.CallSink {
	return func(s llm.CallStat) {
		e.offTurnMu.Lock()
		e.offTurnCalls++
		e.offTurnUs += s.Duration.Microseconds()
		e.offTurnMu.Unlock()
	}
}

// offTurnSnapshot returns the calls and their total wall time in
// microseconds since this Engine was built.
func (e *Engine) offTurnSnapshot() (calls int, us int64) {
	if e == nil {
		return 0, 0
	}
	e.offTurnMu.Lock()
	defer e.offTurnMu.Unlock()
	return e.offTurnCalls, e.offTurnUs
}
