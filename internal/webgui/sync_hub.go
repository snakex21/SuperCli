package webgui

import (
	"context"
	"strconv"
	"sync"
	"time"
)

// syncFrame wraps the existing chat protocol. Snapshots and subscription are
// captured under one lock, so replay cannot lose or duplicate a live delta.
type syncFrame struct {
	Type        string      `json:"type"`
	QueuePaused bool        `json:"queue_paused,omitempty"`
	ProjectPath string      `json:"project_path,omitempty"`
	ProjectName string      `json:"project_name,omitempty"`
	Title       string      `json:"title,omitempty"`
	State       string      `json:"state,omitempty"`
	StartedAtMS int64       `json:"started_at_ms,omitempty"`
	ServerNowMS int64       `json:"server_now_ms,omitempty"`
	SessionID   string      `json:"session_id,omitempty"`
	TurnID      string      `json:"turn_id,omitempty"`
	ClientID    string      `json:"client_id,omitempty"`
	Prompt      string      `json:"prompt,omitempty"`
	Event       *wireEvent  `json:"event,omitempty"`
	Runs        []syncFrame `json:"runs,omitempty"`
	Events      []wireEvent `json:"events,omitempty"`
}

type syncIdentity struct{ turnID, clientID string }
type syncIdentityKey struct{}
type liveSyncRun struct {
	info     syncFrame
	events   []wireEvent
	cancel   context.CancelFunc
	terminal bool
}
type syncSubscriber struct {
	session, client string
	events          chan syncFrame
}
type syncHub struct {
	mu          sync.Mutex
	closed      bool
	sequence    uint64
	runs        map[string]*liveSyncRun
	subscribers map[*syncSubscriber]struct{}
}

func (h *syncHub) publishLocked(frame syncFrame) {
	frame.ServerNowMS = time.Now().UnixMilli()
	for sub := range h.subscribers {
		if frame.Type == "run_event" && (frame.SessionID != sub.session || (sub.client != "" && frame.ClientID == sub.client)) {
			continue
		}
		select {
		case sub.events <- frame:
		default:
			// Never let a suspended phone block generation. Reconnect gets a snapshot.
			delete(h.subscribers, sub)
			close(sub.events)
		}
	}
}
func (h *syncHub) changed() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.publishLocked(syncFrame{Type: "sessions_changed"})
}
func (h *syncHub) start(session, prompt string, identity syncIdentity, cancel context.CancelFunc, details ...syncFrame) *liveSyncRun {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.runs == nil {
		h.runs = make(map[string]*liveSyncRun)
	}
	h.sequence++
	if identity.turnID == "" {
		identity.turnID = "server-" + strconv.FormatUint(h.sequence, 10)
	}
	run := &liveSyncRun{info: syncFrame{Type: "run_started", StartedAtMS: time.Now().UnixMilli(), SessionID: session, TurnID: identity.turnID, ClientID: identity.clientID, Prompt: prompt}, cancel: cancel}
	if len(details) > 0 {
		run.info.ProjectPath, run.info.ProjectName, run.info.Title = details[0].ProjectPath, details[0].ProjectName, details[0].Title
	}
	run.info.State = "running"
	h.runs[session] = run
	h.publishLocked(run.info)
	h.publishLocked(syncFrame{Type: "sessions_changed"})
	return run
}
func (h *syncHub) event(run *liveSyncRun, event wireEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.runs[run.info.SessionID] != run {
		return
	}
	if event.At == 0 {
		event.At = time.Now().UnixMilli()
	}
	if event.Type == "question_closed" {
		// Resolved questions must not reappear when another device reconnects.
		kept := run.events[:0]
		for _, old := range run.events {
			if old.Type == "question" && old.Question != nil && old.Question.ID == event.ID {
				continue
			}
			kept = append(kept, old)
		}
		run.events = kept
	}
	last := len(run.events) - 1
	if (event.Type == "message" || event.Type == "reasoning") && last >= 0 && run.events[last].Type == event.Type {
		run.events[last].Text += event.Text
		run.events[last].ReasoningTok += event.ReasoningTok
	} else {
		run.events = append(run.events, event)
	}
	if event.Type == "question" || event.Type == "question_closed" {
		run.info.State = "running"
		for _, pending := range run.events {
			if pending.Type == "question" {
				run.info.State = "waiting_answer"
				break
			}
		}
		update := run.info
		update.Type, update.Prompt = "run_updated", ""
		h.publishLocked(update)
	}
	if event.Type == "done" || event.Type == "error" {
		run.terminal = true
		if event.Type == "error" || event.QueuePaused {
			run.info.QueuePaused = true
		}
	}
	frame := run.info
	frame.Type, frame.Prompt, frame.Event = "run_event", "", &event
	h.publishLocked(frame)
}
func (h *syncHub) finish(run *liveSyncRun, err error) {
	if err != nil {
		h.mu.Lock()
		terminal := run.terminal
		h.mu.Unlock()
		if !terminal {
			h.event(run, wireEvent{Type: "error", Err: err.Error()})
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.runs[run.info.SessionID] != run {
		return
	}
	delete(h.runs, run.info.SessionID)
	run.events = nil
	frame := run.info
	frame.Type, frame.Prompt = "run_finished", ""
	h.publishLocked(frame)
	h.publishLocked(syncFrame{Type: "sessions_changed"})
}
func (h *syncHub) subscribe(session, client string) (*syncSubscriber, syncFrame, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subscribers == nil {
		h.subscribers = make(map[*syncSubscriber]struct{})
	}
	sub := &syncSubscriber{session: session, client: client, events: make(chan syncFrame, 128)}
	snapshot := syncFrame{Type: "snapshot", ServerNowMS: time.Now().UnixMilli(), Runs: []syncFrame{}}
	for id, run := range h.runs {
		snapshot.Runs = append(snapshot.Runs, run.info)
		if id == session && (client == "" || client != run.info.ClientID) {
			snapshot.Events = append([]wireEvent(nil), run.events...)
		}
	}
	if h.closed {
		close(sub.events)
	} else {
		h.subscribers[sub] = struct{}{}
	}
	return sub, snapshot, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := h.subscribers[sub]; ok {
			delete(h.subscribers, sub)
			close(sub.events)
		}
	}
}
func (h *syncHub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for sub := range h.subscribers {
		delete(h.subscribers, sub)
		close(sub.events)
	}
}

func (h *syncHub) stop(session, turn string) bool {
	h.mu.Lock()
	run := h.runs[session]
	if run == nil || run.info.TurnID != turn {
		h.mu.Unlock()
		return false
	}
	run.info.QueuePaused = true
	cancel := run.cancel
	h.mu.Unlock()
	cancel()
	return true
}
