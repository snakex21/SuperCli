package webgui

import (
	"context"
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

// errNoActiveProvider is a sentinel, not a sentence: the browser renders it
// from the chat.noProvider catalog key so the notice follows the UI language.
var errNoActiveProvider = errors.New("no active AI provider")

func (e *Engine) runStream(ctx context.Context, prompt, sessionID, userAddon string, emit func(wireEvent)) error {
	return e.runStreamWithImages(ctx, prompt, sessionID, userAddon, nil, emit)
}

func (e *Engine) runStreamWithImages(ctx context.Context, prompt, sessionID, userAddon string, images []llm.ImageRef, emit func(wireEvent)) error {
	// Echo is a deliberate CLI/development provider. It repeats the complete
	// model input verbatim, including private cross-session recall and other
	// internal add-ons. A branded end-user app must never use it as a silent
	// fallback when configuration loading fails.
	_, chatReady := e.ProviderStatus()
	if !chatReady {
		return errNoActiveProvider
	}
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
	// Cross-session memory is persisted from the already-written transcript,
	// with no summarizer/model call. Do it on every exit: the helper skips
	// empty/meaningless runs, and a local SQLite read+upsert is tiny compared
	// with provider latency.
	defer func() {
		memoryCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		e.saveWebSessionCapsule(memoryCtx, sid)
	}()
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
	assistantSeqBefore := 0
	telemetry := systats.NewMemory()
	if us, openErr := e.sessionStore(); openErr == nil {
		usageStore = us
		assistantSeqBefore, _ = usageStore.LatestMessageSeq(ctx, sid, string(llm.RoleAssistant))
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
	// Pull only a few relevant capsules from OTHER conversations. This is a
	// local FTS5 lookup (no model/embedding call), capped to a few hundred
	// tokens so remembering old work does not add perceptible drag.
	recallAddon := e.webRelevantSessionMemory(ctx, home, prompt, sid, webSessionRecallTokens)
	combinedAddon := strings.TrimSpace(userAddon)
	if recallAddon != "" {
		if combinedAddon != "" {
			combinedAddon += "\n\n"
		}
		combinedAddon += recallAddon
	}
	if combinedAddon != "" {
		loop.SetNextUserAddon(combinedAddon)
	}
	if len(images) > 0 {
		loop.SetNextUserImages(images)
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
		progressC = progressTimer.C
	}
	pauseProgress := func() {
		if progressTimer == nil {
			return
		}
		progressC = nil
		if !progressTimer.Stop() {
			select {
			case <-progressTimer.C:
			default:
			}
		}
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
	sawVisibleReasoning := false
	turnSaved := false
	terminalUsage := agent.Usage{}
	// Per-turn tool diagnostics: ToolResultEvent carries no tool name, so
	// tool call IDs are paired to names as they stream in. The diag lands
	// in session_turns.tool_diag_json, where a failing run shows its
	// signature (which tool, how often, last message) without a transcript.
	toolNames := map[string]string{}
	var turnDiag session.TurnToolDiag
	recordToolResult := func(ev agent.ToolResultEvent) {
		name := toolNames[ev.ID]
		if ev.Err != nil {
			toolFailures++
			if name == "" {
				name = "?"
			}
			if turnDiag.Failures == nil {
				turnDiag.Failures = map[string]int{}
			}
			turnDiag.Failures[name]++
			if msg := ev.Err.Error(); msg != "" {
				if turnDiag.Messages == nil {
					turnDiag.Messages = map[string]string{}
				}
				turnDiag.Messages[name] = truncateDiag(msg, 200)
			}
			return
		}
		// Stall signature: a search that returns nothing is normal once,
		// a run full of them is a discovery loop (rg-less fallback used to
		// lie about regex queries — this counter is what surfaces that).
		if name == "search_code" && strings.TrimSpace(ev.Output) == "no matches" {
			turnDiag.NoOpSearches++
		}
	}
	var turnFileChanges []checkpoint.FileChange
	finishCheckpoint := func() (string, []checkpoint.FileChange) {
		if checkpointTurn == nil {
			// A terminal done/error event already received the completed
			// checkpoint changes. Channel shutdown must not emit them again as
			// a second standalone file_changes event.
			return "", nil
		}
		turn := checkpointTurn
		checkpointTurn = nil
		finishCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if usageStore != nil {
			if userSeq, seqErr := usageStore.LatestMessageSeq(finishCtx, sid, string(llm.RoleUser)); seqErr == nil {
				turn.SetUserSeq(userSeq)
			} else {
				log.Printf("checkpoint user sequence: %v", seqErr)
			}
		}
		record, finishErr := turn.Complete(finishCtx)
		if finishErr != nil {
			log.Printf("checkpoint complete: %v", finishErr)
			return "", nil
		}
		if record == nil {
			return "", nil
		}
		turnFileChanges = append([]checkpoint.FileChange(nil), record.Changes...)
		return record.ID, turnFileChanges
	}
	saveTurnSummary := func() {
		if turnSaved || usageStore == nil {
			return
		}
		turns := telemetry.Snapshot()
		calls := telemetry.Calls()
		if len(turns) == 0 && len(calls) == 0 && toolCalls == 0 {
			return
		}
		saveCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		assistantSeq, seqErr := usageStore.LatestMessageSeq(saveCtx, sid, string(llm.RoleAssistant))
		if seqErr != nil {
			log.Printf("web turn summary assistant sequence: session=%q: %v", sid, seqErr)
			return
		}
		// A provider can fail before producing any assistant message. Never
		// attach that failed attempt to (and overwrite) the previous answer.
		if assistantSeq <= assistantSeqBefore {
			return
		}
		// One aggregation of the loop's own telemetry: token fallback and the
		// helper-inference counters read from the same snapshot, so the aux
		// numbers can never come from a second clock.
		total := systats.Sum(turns)
		usage := terminalUsage
		if usage.Input == 0 && usage.Output == 0 {
			usage.Input = total.TokensIn
			usage.Output = total.TokensOut
			usage.Total = usage.Input + usage.Output
		}
		steps := len(turns)
		for _, call := range calls {
			if call.Step > steps {
				steps = call.Step
			}
		}
		failedCalls, canceledCalls, backgroundCalls, helperCalls := summarizeTelemetryCalls(calls)
		storedChanges := make([]session.FileChange, 0, len(turnFileChanges))
		for _, change := range turnFileChanges {
			storedChanges = append(storedChanges, session.FileChange{Path: change.Path, Kind: change.Kind})
		}
		if saveErr := usageStore.AppendTurnSummary(saveCtx, session.TurnSummary{
			SessionID: sid, AssistantSeq: assistantSeq,
			DurationMS: time.Since(runStarted).Milliseconds(),
			Input:      int64(usage.Input), Output: int64(usage.Output),
			CachedInput: int64(usage.Cached), Reasoning: int64(usage.Reasoning),
			HasCachedInput: usage.Cached > 0, HasReasoning: usage.Reasoning > 0,
			ToolCalls: toolCalls, ToolFailures: toolFailures, Steps: steps,
			ModelCalls: len(calls), FailedCalls: failedCalls, CanceledCalls: canceledCalls,
			BackgroundCalls: backgroundCalls, HelperCalls: helperCalls,
			AuxCalls: total.AuxCalls, AuxUs: total.AuxUs,
			Phases: systats.SumPhases(turns), FileChanges: storedChanges,
			ToolDiag: turnDiag,
		}); saveErr != nil {
			// Telemetry must never fail or delay the user's answer/error.
			log.Printf("web turn summary: session=%q: %v", sid, saveErr)
			return
		}
		turnSaved = true
	}
	// Context cancellation and semantic progress timeouts return before the
	// loop's terminal event can always be observed. Persist whatever completed
	// phase/call data exists so failed runs are not invisible in telemetry.
	defer saveTurnSummary()
	for {
		select {
		case <-ctx.Done():
			flushMessages()
			checkpointID, changes := finishCheckpoint()
			if len(changes) > 0 {
				send(wireEvent{Type: "file_changes", CheckpointID: checkpointID, FileChanges: changes})
			}
			return ctx.Err()
		case <-progressC:
			flushMessages()
			checkpointID, changes := finishCheckpoint()
			if len(changes) > 0 {
				send(wireEvent{Type: "file_changes", CheckpointID: checkpointID, FileChanges: changes})
			}
			return fmt.Errorf("provider produced no model progress for %s", progressTimeout)
		case <-messageTimerC:
			messageTimerC = nil
			coalescer.Flush()
		case ev, ok := <-ch:
			if !ok {
				flushMessages()
				checkpointID, changes := finishCheckpoint()
				if len(changes) > 0 {
					send(wireEvent{Type: "file_changes", CheckpointID: checkpointID, FileChanges: changes})
				}
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
			if _, ok := ev.(agent.ReasoningEvent); ok {
				sawVisibleReasoning = true
			}
			if _, ok := ev.(agent.ToolCallEvent); ok {
				toolCalls++
			}
			if call, ok := ev.(agent.ToolCallEvent); ok {
				toolNames[call.ID] = call.Name
			}
			if result, ok := ev.(agent.ToolResultEvent); ok {
				recordToolResult(result)
			}
			checkpointID := ""
			var fileChanges []checkpoint.FileChange
			_, doneEvent := ev.(agent.DoneEvent)
			_, errorEvent := ev.(agent.ErrorEvent)
			if doneEvent || errorEvent {
				flushMessages()
				checkpointID, fileChanges = finishCheckpoint()
			}
			if done, ok := ev.(agent.DoneEvent); ok {
				terminalUsage = done.Usage
				// The provider reported reasoning tokens but streamed no
				// thinking text. Announce the count only; the front-end turns
				// reasoning_tok into a sentence in the user's UI language.
				// Building that prose here would hardcode one language into a
				// layer that has no string catalog.
				if !sawVisibleReasoning && done.Usage.Reasoning > 0 {
					send(wireEvent{Type: "reasoning", ReasoningTok: done.Usage.Reasoning})
					sawVisibleReasoning = true
				}
				saveTurnSummary()
			}
			if failed, ok := ev.(agent.ErrorEvent); ok {
				terminalUsage = failed.Usage
				if failed.Err != nil {
					turnDiag.Terminal = truncateDiag(failed.Err.Error(), 300)
				}
				saveTurnSummary()
			}
			if w, keep := toWireEvent(ev); keep {
				w.CheckpointID = checkpointID
				w.FileChanges = fileChanges
				send(w)
			}
		case ev := <-external:
			resetProgress()
			if w, keep := toWireEvent(ev); keep {
				send(w)
			}
		case req := <-askCh:
			// Waiting for a human answer is not provider inactivity. Suspend the
			// model-progress watchdog until ask_user completes and the agent emits
			// its tool result / next model event. ask_user has its own much longer
			// interaction timeout and the request context still cancels promptly.
			pauseProgress()
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

// truncateDiag caps a diagnostic string at max runes (with an ellipsis),
// keeping the per-turn diag column compact: the goal is to distinguish
// failure KINDS, not to duplicate the transcript.
func truncateDiag(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}
