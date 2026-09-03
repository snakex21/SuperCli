package webgui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"supercli/internal/agent"
	"supercli/internal/checkpoint"
)

// wireEvent is the JSON shape sent to the browser for each agent
// loop event. Type discriminates the union so the front-end can
// render messages, tool calls, results, and terminal events
// differently. Empty fields are omitted to keep the stream compact.
type wireEvent struct {
	Type string `json:"type"`
	// Text carries assistant message chunks (type "message") and
	// reflection/marker text.
	Text string `json:"text,omitempty"`
	// Tool fields (type "tool_call" / "tool_result"). Name doubles as
	// the worker agent kind on type "worker".
	Name   string `json:"name,omitempty"`
	Args   string `json:"args,omitempty"`
	ID     string `json:"id,omitempty"`
	Output string `json:"output,omitempty"`
	Err    string `json:"err,omitempty"`
	// Status carries the worker outcome on type "worker".
	Status string `json:"status,omitempty"`
	Kind   string `json:"kind,omitempty"`
	CallID string `json:"call_id,omitempty"`
	Tool   string `json:"tool,omitempty"`
	// Usage fields (type "done").
	TokIn    int `json:"tok_in,omitempty"`
	TokOut   int `json:"tok_out,omitempty"`
	TokTotal int `json:"tok_total,omitempty"`
	// TokCached is the raw cached-prompt token count (type "done"),
	// so the front-end can render the TUI-style
	// "cache X · eval Y · gen Z" breakdown without recomputing it
	// from the percentage.
	TokCached int `json:"tok_cached,omitempty"`
	// Observability (type "done"): KV/prompt cache-hit percentage
	// (cached prompt tokens / prompt tokens) and hidden reasoning
	// tokens for the run. Omitted when the backend does not report
	// them, mirroring the TUI cache:/think: badges.
	CacheHitPct  int                     `json:"cache_hit_pct,omitempty"`
	ReasoningTok int                     `json:"reasoning_tok,omitempty"`
	CheckpointID string                  `json:"checkpoint_id,omitempty"`
	FileChanges  []checkpoint.FileChange `json:"file_changes,omitempty"`
	// Step is set on reflection / sisyphus markers.
	Step int `json:"step,omitempty"`
	// SessionID is emitted once at stream start so the browser keeps later
	// prompts in the same persisted conversation.
	SessionID string        `json:"session_id,omitempty"`
	Question  *questionWire `json:"question,omitempty"`
}

// toWireEvent maps a typed agent.Event to its JSON wire form. The
// returned bool is false for events the GUI deliberately drops (they
// have no useful browser rendering yet), so the caller can skip them.
func toWireEvent(ev agent.Event) (wireEvent, bool) {
	switch e := ev.(type) {
	case agent.MessageEvent:
		return wireEvent{Type: "message", Text: e.Text}, true
	case agent.ReasoningEvent:
		return wireEvent{Type: "reasoning", Text: e.Text}, true
	case agent.ToolCallEvent:
		return wireEvent{Type: "tool_call", Name: e.Name, Args: e.Args, ID: e.ID}, true
	case agent.ToolResultEvent:
		w := wireEvent{Type: "tool_result", ID: e.ID, Output: e.Output}
		if e.Err != nil {
			w.Err = e.Err.Error()
		}
		return w, true
	case agent.ReflectionEvent:
		return wireEvent{Type: "reflection", Step: e.Step, Text: e.Text}, true
	case agent.SisyphusEvent:
		return wireEvent{Type: "sisyphus", Step: e.Step, Text: e.Text}, true
	case agent.AutoCompactEvent:
		return wireEvent{Type: "compact", Text: fmt.Sprintf("context compacted (%s): removed %d, ~%d/%d tokens, trigger=%d/%s, estimate=%s, window=%s", e.Reason, e.Removed, e.Estimated, e.Window, e.Threshold, e.ThresholdSource, e.EstimateSource, e.WindowSource)}, true
	case agent.ToolResultsPrunedEvent:
		return wireEvent{Type: "notice", Text: fmt.Sprintf("pruned %d old tool result(s), reclaimed ~%d tokens (trigger=%d/%s)", e.Pruned, e.Reclaimed, e.Threshold, e.ThresholdSource)}, true
	case agent.NoticeEvent:
		return wireEvent{Type: "notice", Text: e.Text}, true
	case agent.WorkerNotificationEvent:
		// Background worker lifecycle (task delegation). Name carries
		// the agent kind, Status the outcome, Output the short summary.
		return wireEvent{Type: "worker", ID: e.TaskID, Name: e.Agent, Status: e.Status, Output: e.Summary, Text: e.Text}, true
	case agent.WorkerProgressEvent:
		return wireEvent{Type: "worker_progress", ID: e.TaskID, Name: e.Agent, Kind: e.Kind,
			CallID: e.CallID, Tool: e.Tool, Args: e.Args, Output: e.Output, Err: e.Err}, true
	case agent.DraftUsedEvent:
		return wireEvent{Type: "notice", Text: fmt.Sprintf(
			"draft-verify: %s · draft %s → verdict %s · saved ~%d tok",
			e.Decision, e.DraftModel, e.VerifierModel, e.Savings)}, true
	case agent.MessagesHiddenEvent:
		return wireEvent{Type: "notice", Text: fmt.Sprintf("hid %d old message(s) from the model's view (%s)", e.Count, e.Reason)}, true
	case agent.ConsultEvent:
		txt := fmt.Sprintf("consult: %d candidate(s), winner %s", e.CandidateCount, e.WinnerProvider)
		if e.AllFailed {
			txt = "consult: all candidates failed"
		}
		return wireEvent{Type: "notice", Text: txt}, true
	case agent.DoneEvent:
		w := wireEvent{
			Type:         "done",
			TokIn:        e.Usage.Input,
			TokOut:       e.Usage.Output,
			TokTotal:     e.Usage.Total,
			TokCached:    e.Usage.Cached,
			ReasoningTok: e.Usage.Reasoning,
		}
		if e.Usage.Input > 0 && e.Usage.Cached > 0 {
			w.CacheHitPct = e.Usage.Cached * 100 / e.Usage.Input
		}
		return w, true
	case agent.ErrorEvent:
		msg := ""
		if e.Err != nil {
			msg = e.Err.Error()
		}
		return wireEvent{Type: "error", Err: msg}, true
	default:
		return wireEvent{}, false
	}
}

// marshal returns the wire event as a single JSON line. A marshal
// failure (should never happen for these flat structs) is reported
// as an error wire event so the stream still tells the user
// something went wrong.
func (w wireEvent) marshal() []byte {
	b, err := json.Marshal(w)
	if err != nil {
		fallback, _ := json.Marshal(wireEvent{Type: "error", Err: "encode: " + err.Error()})
		return fallback
	}
	return b
}

const (
	// A 40 ms window is below the normal visual rendering cadence while
	// collapsing token-at-a-time providers into far fewer JSON/SSE writes.
	messageCoalesceWindow = 40 * time.Millisecond
	// Large bursts flush eagerly so the batching layer never becomes an
	// output-sized buffer or adds noticeable latency on very fast backends.
	messageCoalesceBytes = 4 * 1024
)

// messageCoalescer combines consecutive assistant text or reasoning chunks.
// The two channels are never merged with one another. Every
// semantic boundary (tool, worker, question, notice, done, error) first flushes
// pending text and is then emitted immediately, preserving event order.
type messageCoalescer struct {
	emit        func(wireEvent)
	pendingType string
	pending     strings.Builder
}

func (c *messageCoalescer) Pending() bool { return c.pending.Len() > 0 }

// Push returns true when a new timed batch was started.
func (c *messageCoalescer) Push(ev wireEvent) (started bool) {
	if ev.Type == "message" || ev.Type == "reasoning" {
		if c.Pending() && c.pendingType != ev.Type {
			c.Flush()
		}
		started = !c.Pending()
		if started {
			c.pendingType = ev.Type
		}
		c.pending.WriteString(ev.Text)
		if c.pending.Len() >= messageCoalesceBytes {
			c.Flush()
			return false
		}
		return started && c.Pending()
	}
	c.Flush()
	c.emit(ev)
	return false
}

func (c *messageCoalescer) Flush() {
	if !c.Pending() {
		return
	}
	text := c.pending.String()
	typ := c.pendingType
	c.pending.Reset()
	c.pendingType = ""
	c.emit(wireEvent{Type: typ, Text: text})
}



// runStream runs one prompt on a fresh loop and forwards every
// translated event to emit. It blocks until the loop's event channel
// closes or ctx is cancelled. emit is called from this goroutine; the
// HTTP handler is responsible for flushing to the client.
