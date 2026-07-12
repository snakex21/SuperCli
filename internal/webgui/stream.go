package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
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
	CacheHitPct  int `json:"cache_hit_pct,omitempty"`
	ReasoningTok int `json:"reasoning_tok,omitempty"`
	// Step is set on reflection / sisyphus markers.
	Step int `json:"step,omitempty"`
	// SessionID is emitted once at stream start so the browser keeps later
	// prompts in the same persisted conversation.
	SessionID string `json:"session_id,omitempty"`
}

// toWireEvent maps a typed agent.Event to its JSON wire form. The
// returned bool is false for events the GUI deliberately drops (they
// have no useful browser rendering yet), so the caller can skip them.
func toWireEvent(ev agent.Event) (wireEvent, bool) {
	switch e := ev.(type) {
	case agent.MessageEvent:
		return wireEvent{Type: "message", Text: e.Text}, true
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
		return wireEvent{Type: "compact", Text: fmt.Sprintf("context compacted (%s): removed %d, window %d", e.Reason, e.Removed, e.Window)}, true
	case agent.ToolResultsPrunedEvent:
		return wireEvent{Type: "notice", Text: fmt.Sprintf("pruned %d old tool result(s), reclaimed ~%d tokens", e.Pruned, e.Reclaimed)}, true
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

// runStream runs one prompt on a fresh loop and forwards every
// translated event to emit. It blocks until the loop's event channel
// closes or ctx is cancelled. emit is called from this goroutine; the
// HTTP handler is responsible for flushing to the client.
func (e *Engine) runStream(ctx context.Context, prompt, sessionID string, emit func(wireEvent)) error {
	home := e.Home()
	initial, writer, closeStore, sid, err := e.sessionState(ctx, prompt, sessionID, home)
	if err != nil {
		return err
	}
	defer closeStore()
	if strings.TrimSpace(sessionID) == "" {
		// Fresh session: the LLM title summary runs only after this
		// answer has fully streamed AND the session sat idle — the
		// deterministic local title from sessionState covers the gap.
		defer e.titles.Schedule(sid, prompt)
	} else {
		// New activity on an existing session: whatever title work is
		// pending or in flight for it is stale — cancel, don't compete
		// with the foreground answer.
		e.titles.Cancel(sid)
	}
	emit(wireEvent{Type: "session", SessionID: sid})

	// A separate lightweight store handle records one row per actual model
	// call. The session writer still owns messages and legacy aggregates.
	// The recorder rides the run context as an llm.CallSink: the single
	// factory-built metered provider reports every call (coordinator
	// steps AND delegated workers) here — no second wrapper.
	var usageStore *session.Store
	if us, openErr := session.OpenStore(e.dataDir); openErr == nil {
		usageStore = us
		defer usageStore.Close()
		if sink := e.usageCallSink(usageStore, sid); sink != nil {
			ctx = llm.WithCallSink(ctx, sink)
		}
	}
	loop, err := e.newLoopWithSessionAtUsage(initial, writer, home)
	if err != nil {
		return fmt.Errorf("build loop: %w", err)
	}
	// Worker completions, worker-backend fallback notices and draft-verify
	// telemetry are emitted through Loop's external sink rather than the main
	// Run channel. Bridge that sink into the same SSE response so delegations
	// are visible in the transcript and Workers panel.
	external := make(chan agent.Event, 32)
	loop.SetExternalSink(external)
	defer loop.SetExternalSink(nil)
	// Preflight repo context (config preflight_repo, default ON): the
	// block rides the FIRST user message of a session only — resumed
	// conversations already paid for it. The notice makes the cost
	// visible in the transcript, mirroring the CLI's startup log line.
	if len(initial) == 0 {
		if block, tokens := e.preflightBlockAt(home); block != "" {
			loop.SetNextUserAddon(block)
			emit(wireEvent{Type: "notice", Text: fmt.Sprintf("preflight: repo context ~%d tok (rides this message)", tokens)})
		}
	}
	ch, err := loop.Run(ctx, prompt)
	if err != nil {
		return fmt.Errorf("run: %w", err)
	}
	// Provider transports may keep an HTTP connection alive with comment-only
	// heartbeats while producing no model/tool progress. The transport-level
	// idle reader sees those bytes and cannot distinguish that state from a
	// healthy stream, so enforce the configured inactivity limit at the agent
	// event layer too. This guarantees a terminal error instead of an endless
	// spinner when an upstream job is wedged.
	e.mu.RLock()
	progressTimeout := e.cfg.Timeout
	e.mu.RUnlock()
	var progressTimer *time.Timer
	var progressC <-chan time.Time
	if progressTimeout > 0 {
		progressTimer = time.NewTimer(progressTimeout)
		progressC = progressTimer.C
		defer progressTimer.Stop()
	}
	resetProgress := func() {
		if progressTimer == nil {
			return
		}
		if !progressTimer.Stop() {
			select {
			case <-progressTimer.C:
			default:
			}
		}
		progressTimer.Reset(progressTimeout)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-progressC:
			return fmt.Errorf("provider produced no model progress for %s", progressTimeout)
		case ev, ok := <-ch:
			if !ok {
				// Synchronous task completion may enqueue its external marker just
				// before the parent turn closes. Drain what is already available.
				for {
					select {
					case extra := <-external:
						if w, keep := toWireEvent(extra); keep {
							emit(w)
						}
					default:
						return nil
					}
				}
			}
			resetProgress()
			if w, keep := toWireEvent(ev); keep {
				emit(w)
			}
		case ev := <-external:
			resetProgress()
			if w, keep := toWireEvent(ev); keep {
				emit(w)
			}
		}
	}
}

// sessionState opens the persistent session store, creates a new session when
// no id was supplied, or loads existing messages when the browser continues a
// chat. The returned close function must be called after the run completes.
func (e *Engine) sessionState(ctx context.Context, prompt, requestedID, home string) ([]llm.Message, agent.SessionWriter, func(), string, error) {
	store, err := session.OpenStore(e.dataDir)
	if err != nil {
		return nil, nil, func() {}, "", fmt.Errorf("open session store: %w", err)
	}
	closeStore := func() { _ = store.Close() }

	requestedID = strings.TrimSpace(requestedID)
	provider, model, reasoning := e.RuntimeSelection()
	if requestedID == "" {
		// The first title is deterministic and local — set right here,
		// zero inference, so the session is named instantly and the
		// model slot stays free for the actual answer. The nicer LLM
		// title runs later, after the stream + idle (see title.go).
		title := summarizeHistoryMessage(prompt, 80)
		sess, err := store.Create(home, model, title)
		if err != nil {
			closeStore()
			return nil, nil, func() {}, "", fmt.Errorf("create session: %w", err)
		}
		if err := store.SetRuntime(sess.ID, provider, model, reasoning); err != nil {
			closeStore()
			return nil, nil, func() {}, "", fmt.Errorf("save session runtime: %w", err)
		}
		return nil, session.NewWriter(store, sess.ID), closeStore, sess.ID, nil
	}

	meta, err := store.Get(requestedID)
	if err != nil {
		closeStore()
		return nil, nil, func() {}, "", fmt.Errorf("resume session: %w", err)
	}
	if !sameSessionWorkspace(meta.Cwd, home) {
		closeStore()
		return nil, nil, func() {}, "", fmt.Errorf("resume session: %w", errSessionOutsideWorkspace)
	}
	if err := store.SetRuntime(requestedID, provider, model, reasoning); err != nil {
		closeStore()
		return nil, nil, func() {}, "", fmt.Errorf("save session runtime: %w", err)
	}
	initial, err := store.ReadModelContext(ctx, requestedID)
	if err != nil {
		closeStore()
		return nil, nil, func() {}, "", fmt.Errorf("read session messages: %w", err)
	}
	return initial, session.NewWriter(store, requestedID), closeStore, requestedID, nil
}
