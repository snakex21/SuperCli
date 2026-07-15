package webgui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"supercli/internal/agent"
	"supercli/internal/checkpoint"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	systats "supercli/internal/system/stats"
	"supercli/internal/tools"
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
	CacheHitPct  int    `json:"cache_hit_pct,omitempty"`
	ReasoningTok int    `json:"reasoning_tok,omitempty"`
	CheckpointID string `json:"checkpoint_id,omitempty"`
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

const (
	// A 40 ms window is below the normal visual rendering cadence while
	// collapsing token-at-a-time providers into far fewer JSON/SSE writes.
	messageCoalesceWindow = 40 * time.Millisecond
	// Large bursts flush eagerly so the batching layer never becomes an
	// output-sized buffer or adds noticeable latency on very fast backends.
	messageCoalesceBytes = 4 * 1024
)

// messageCoalescer combines only consecutive assistant text chunks. Every
// semantic boundary (tool, worker, question, notice, done, error) first flushes
// pending text and is then emitted immediately, preserving event order.
type messageCoalescer struct {
	emit    func(wireEvent)
	pending strings.Builder
}

func (c *messageCoalescer) Pending() bool { return c.pending.Len() > 0 }

// Push returns true when a new timed batch was started.
func (c *messageCoalescer) Push(ev wireEvent) (started bool) {
	if ev.Type == "message" {
		started = !c.Pending()
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
	c.pending.Reset()
	c.emit(wireEvent{Type: "message", Text: text})
}

// runStream runs one prompt on a fresh loop and forwards every
// translated event to emit. It blocks until the loop's event channel
// closes or ctx is cancelled. emit is called from this goroutine; the
// HTTP handler is responsible for flushing to the client.
func (e *Engine) runStream(ctx context.Context, prompt, sessionID, userAddon string, emit func(wireEvent)) error {
	runStarted := time.Now()
	askCh := make(chan tools.AskRequest, 3)
	activeQuestions := []string{}
	defer func() {
		for _, id := range activeQuestions {
			e.cancelQuestion(id)
		}
	}()
	home := e.Home()
	initial, writer, sid, err := e.sessionState(ctx, prompt, sessionID, home)
	if err != nil {
		return err
	}
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
	telemetry := systats.NewMemory()
	if us, openErr := e.sessionStore(); openErr == nil {
		usageStore = us
		if sink := e.usageCallSink(usageStore, sid); sink != nil {
			ctx = llm.WithCallSink(ctx, sink)
		}
	}
	ctx = llm.WithCallSink(ctx, telemetryCallSink(telemetry))
	var checkpointTurn *checkpoint.Turn
	if manager, openErr := e.checkpointManager(home); openErr == nil {
		checkpointTurn = manager.NewTurn(sid, prompt)
	} else if !errors.Is(openErr, checkpoint.ErrUnavailable) {
		log.Printf("checkpoint open: %v", openErr)
	}
	loop, err := e.newLoopWithSessionAtUsageInteractive(initial, writer, home, askCh, checkpointTurn, telemetry)
	if err != nil {
		return fmt.Errorf("build loop: %w", err)
	}
	if strings.TrimSpace(userAddon) != "" {
		loop.SetNextUserAddon(userAddon)
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
	if shouldAttachPreflight(initial, prompt) {
		if block, tokens := e.preflightBlockAt(home); block != "" {
			loop.SetNextCoordinatorAddon(block)
			emit(wireEvent{Type: "notice", Text: fmt.Sprintf("preflight: repo context ~%d tok (next project turn)", tokens)})
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
	coalescer := &messageCoalescer{emit: emit}
	var messageTimer *time.Timer
	var messageTimerC <-chan time.Time
	stopMessageTimer := func() {
		messageTimerC = nil
		if messageTimer == nil {
			return
		}
		if !messageTimer.Stop() {
			select {
			case <-messageTimer.C:
			default:
			}
		}
	}
	flushMessages := func() {
		stopMessageTimer()
		coalescer.Flush()
	}
	send := func(ev wireEvent) {
		started := coalescer.Push(ev)
		if !coalescer.Pending() {
			stopMessageTimer()
			return
		}
		if started {
			if messageTimer == nil {
				messageTimer = time.NewTimer(messageCoalesceWindow)
			} else {
				messageTimer.Reset(messageCoalesceWindow)
			}
			messageTimerC = messageTimer.C
		}
	}
	defer flushMessages()
	toolCalls := 0
	toolFailures := 0
	turnSaved := false
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-progressC:
			return fmt.Errorf("provider produced no model progress for %s", progressTimeout)
		case <-messageTimerC:
			messageTimerC = nil
			coalescer.Flush()
		case ev, ok := <-ch:
			if !ok {
				// Synchronous task completion may enqueue its external marker just
				// before the parent turn closes. Drain what is already available.
				for {
					select {
					case extra := <-external:
						if w, keep := toWireEvent(extra); keep {
							send(w)
						}
					default:
						return nil
					}
				}
			}
			resetProgress()
			if _, ok := ev.(agent.ToolCallEvent); ok {
				toolCalls++
			}
			if result, ok := ev.(agent.ToolResultEvent); ok && result.Err != nil {
				toolFailures++
			}
			checkpointID := ""
			if _, ok := ev.(agent.DoneEvent); ok && checkpointTurn != nil {
				finishCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				if usageStore != nil {
					if userSeq, seqErr := usageStore.LatestMessageSeq(finishCtx, sid, string(llm.RoleUser)); seqErr == nil {
						checkpointTurn.SetUserSeq(userSeq)
					} else {
						log.Printf("checkpoint user sequence: %v", seqErr)
					}
				}
				record, finishErr := checkpointTurn.Complete(finishCtx)
				cancel()
				if finishErr != nil {
					log.Printf("checkpoint complete: %v", finishErr)
				} else if record != nil {
					checkpointID = record.ID
				}
			}
			if done, ok := ev.(agent.DoneEvent); ok && !turnSaved && usageStore != nil {
				turnSaved = true
				turns := telemetry.Snapshot()
				calls := telemetry.Calls()
				failedCalls, canceledCalls, backgroundCalls, helperCalls := summarizeTelemetryCalls(calls)
				if saveErr := usageStore.AppendTurnSummary(context.Background(), session.TurnSummary{
					SessionID: sid, DurationMS: time.Since(runStarted).Milliseconds(),
					Input: int64(done.Usage.Input), Output: int64(done.Usage.Output),
					CachedInput: int64(done.Usage.Cached), Reasoning: int64(done.Usage.Reasoning),
					HasCachedInput: done.Usage.Cached > 0, HasReasoning: done.Usage.Reasoning > 0,
					ToolCalls: toolCalls, ToolFailures: toolFailures, Steps: len(turns),
					ModelCalls: len(calls), FailedCalls: failedCalls, CanceledCalls: canceledCalls,
					BackgroundCalls: backgroundCalls, HelperCalls: helperCalls,
					Phases: systats.SumPhases(turns),
				}); saveErr != nil {
					// Telemetry must never fail the user's completed answer.
					log.Printf("web turn summary: session=%q: %v", sid, saveErr)
				}
			}
			if w, keep := toWireEvent(ev); keep {
				w.CheckpointID = checkpointID
				send(w)
			}
		case ev := <-external:
			resetProgress()
			if w, keep := toWireEvent(ev); keep {
				send(w)
			}
		case req := <-askCh:
			activeQuestions = append(activeQuestions, req.ID)
			question := e.registerQuestion(req)
			send(wireEvent{Type: "question", Question: &question})
		}
	}
}

// shouldAttachPreflight lets a new WebGUI chat start with cheap smalltalk and
// delays repository context until the first project-like turn. WebGUI builds a
// fresh loop per HTTP request, so we infer whether a previous user turn already
// crossed that boundary from persisted prompts. Ambiguous prompts count as
// project-like: paying once is safer than starving a real task of repo facts.
func shouldAttachPreflight(initial []llm.Message, prompt string) bool {
	routes := agent.DefaultRouteMap()
	for _, msg := range initial {
		if msg.Role != llm.RoleUser || strings.Contains(msg.Content, "<task-notification>") {
			continue
		}
		mode, confident := routes.ClassifyConfident(msg.Content)
		if !confident || mode == agent.RouteCoordinator {
			return false
		}
	}
	mode, confident := routes.ClassifyConfident(prompt)
	return !confident || mode == agent.RouteCoordinator
}

// sessionState opens the persistent session store, creates a new session when
// no id was supplied, or loads existing messages when the browser continues a
// chat. The returned close function must be called after the run completes.
func (e *Engine) sessionState(ctx context.Context, prompt, requestedID, home string) ([]llm.Message, agent.SessionWriter, string, error) {
	store, err := e.sessionStore()
	if err != nil {
		return nil, nil, "", fmt.Errorf("open session store: %w", err)
	}

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
			return nil, nil, "", fmt.Errorf("create session: %w", err)
		}
		if err := store.SetRuntime(sess.ID, provider, model, reasoning); err != nil {
			return nil, nil, "", fmt.Errorf("save session runtime: %w", err)
		}
		return nil, session.NewWriter(store, sess.ID), sess.ID, nil
	}

	meta, err := store.Get(requestedID)
	if err != nil {
		return nil, nil, "", fmt.Errorf("resume session: %w", err)
	}
	if !sameSessionWorkspace(meta.Cwd, home) {
		return nil, nil, "", fmt.Errorf("resume session: %w", errSessionOutsideWorkspace)
	}
	if err := store.SetRuntime(requestedID, provider, model, reasoning); err != nil {
		return nil, nil, "", fmt.Errorf("save session runtime: %w", err)
	}
	initial, err := store.ReadModelContext(ctx, requestedID)
	if err != nil {
		return nil, nil, "", fmt.Errorf("read session messages: %w", err)
	}
	return initial, session.NewWriter(store, requestedID), requestedID, nil
}
