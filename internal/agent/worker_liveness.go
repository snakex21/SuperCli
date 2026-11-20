package agent

import (
	"encoding/json"
	"time"
)

// WorkerLiveness is what a running worker is doing right now and when it
// last made observable progress (a streamed model chunk, a tool call or
// result, tool progress). The GUI shows it so a working agent can be told
// apart from a stalled one ("no progress for 6 min"). Zero for an idle worker.
type WorkerLiveness struct {
	// Phase is "model" (waiting for / streaming the model's answer),
	// "args" (the model is writing a tool call's arguments, e.g. a file's
	// content; the call has not started yet) or "tool" (at least one tool
	// call is executing).
	Phase string
	// Tool names the call being written (Phase "args") or the first tool of
	// the executing batch (Phase "tool").
	Tool string
	// ArgsBytes is the streamed argument size (Phase "args").
	ArgsBytes int
	// OpenCalls is the number of tool calls without a result yet.
	OpenCalls int
	// PhaseSince is when the current phase began.
	PhaseSince time.Time
	// LastProgressAt is the latest observable progress of the run.
	LastProgressAt time.Time
	// ToolProgress is the latest live progress label of the executing tool
	// (e.g. the last output line of a long command), UI-only.
	ToolProgress string
}

// liveRunStarted resets liveness for a new run. Caller holds stateMu.
func (w *Worker) liveRunStarted(now time.Time) {
	w.live = WorkerLiveness{Phase: "model", PhaseSince: now, LastProgressAt: now}
}

// liveRunEnded clears liveness after a run. Caller holds stateMu.
func (w *Worker) liveRunEnded() {
	w.live = WorkerLiveness{}
	w.liveUI = nil
}

// liveObserve records progress for one worker loop event. Caller holds stateMu.
func (w *Worker) liveObserve(ev Event, now time.Time) {
	if w.live.Phase == "" {
		return
	}
	switch e := ev.(type) {
	case MessageEvent, ReasoningEvent, DoneEvent, steeringDeliveryEvent:
		w.live.LastProgressAt = now
	case ToolArgsProgressEvent:
		w.live.LastProgressAt = now
		if w.live.Phase == "tool" {
			break // a later call of the same batch; execution is what runs now
		}
		if w.live.Phase != "args" || w.live.Tool != e.Tool {
			w.live.Phase, w.live.Tool, w.live.PhaseSince, w.live.ToolProgress = "args", e.Tool, now, ""
		}
		w.live.ArgsBytes = e.Bytes
	case ToolCallEvent:
		w.live.LastProgressAt = now
		w.live.OpenCalls++
		w.live.ArgsBytes = 0
		if w.live.Phase != "tool" {
			w.live.Phase, w.live.Tool, w.live.PhaseSince, w.live.ToolProgress = "tool", e.Name, now, ""
		}
	case ToolResultEvent:
		w.live.LastProgressAt = now
		if w.live.OpenCalls > 0 {
			w.live.OpenCalls--
		}
		if w.live.OpenCalls == 0 {
			w.live.Phase, w.live.Tool, w.live.PhaseSince, w.live.ToolProgress = "model", "", now, ""
		}
	case ToolProgressEvent:
		w.live.LastProgressAt = now
		if e.Text != "" {
			w.live.ToolProgress = e.Text
		}
	}
}

// LiveActivity returns the current run's UI journal (the same format that is
// saved with the worker's report), so a view opened or refreshed mid-run can
// rebuild the full tool list instead of showing only calls made afterwards.
// ok is false when the worker has no run in progress.
func (w *Worker) LiveActivity() (data json.RawMessage, ok bool) {
	if w == nil {
		return nil, false
	}
	w.stateMu.RLock()
	ui := w.liveUI
	var copyUI workerUIActivity
	if ui != nil {
		copyUI = *ui
		copyUI.Calls = append([]workerUIToolCall(nil), ui.Calls...)
	}
	w.stateMu.RUnlock()
	if ui == nil {
		return nil, false
	}
	// encode may shed previews of its own copy; the live journal is untouched.
	return copyUI.encode(), true
}
