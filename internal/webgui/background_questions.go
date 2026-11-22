package webgui

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"supercli/internal/tools"
)

// Questions of background workers (1.0.39). A delegated worker runs detached
// from the chat run that started it, so by the time it calls ask_user (or
// needs an approval) that run's question channel has no reader any more.
// Each conversation therefore owns one durable question route:
//
//   - a request is registered like a run's question (same answer endpoint)
//     and kept as pending for its conversation until it is answered, it
//     expires (the asking tool's own timeout closes Done), or a bounded
//     lifetime passes for a request without Done;
//   - it is published to every view of the conversation, counted per
//     conversation in the sync snapshot (a badge in the session list), and
//     served by GET /api/questions?session= so a view that opens the
//     conversation later, or reloads, shows it again.

// backgroundAskBuffer bounds questions queued before the pump publishes them.
const backgroundAskBuffer = 8

// backgroundQuestionMaxAge closes a request that has no Done channel of its
// own (it must never stay registered forever).
var backgroundQuestionMaxAge = 30 * time.Minute

// backgroundQuestionPoll notices an answered request without Done.
var backgroundQuestionPoll = 2 * time.Second

type pendingQuestion struct {
	wire  questionWire
	added time.Time
}

func (bg *sessionBackground) askChannel() chan<- tools.AskRequest {
	if bg == nil {
		return nil
	}
	return bg.ask
}

func (bg *sessionBackground) askPump() {
	for {
		var req tools.AskRequest
		select {
		case req = <-bg.ask:
		case <-bg.stop:
			return
		}
		if req.Done != nil {
			select {
			case <-req.Done:
				continue // expired while queued
			default:
			}
		}
		question := bg.e.registerQuestion(req)
		bg.e.addPendingQuestion(bg.sid, question)
		bg.e.syncHub.background(bg.sid, wireEvent{Type: "question", Question: &question, At: time.Now().UnixMilli()})
		go bg.watchQuestion(req)
	}
}

// watchQuestion removes the question when it is answered or expires. A
// request with its own Done ends with it (the asking tool owns its timeout);
// only a request without Done gets a bounded lifetime here. Engine shutdown
// ends the watch.
func (bg *sessionBackground) watchQuestion(req tools.AskRequest) {
	var deadlineC <-chan time.Time
	if req.Done == nil {
		deadline := time.NewTimer(backgroundQuestionMaxAge)
		defer deadline.Stop()
		deadlineC = deadline.C
	}
	poll := time.NewTicker(backgroundQuestionPoll)
	defer poll.Stop()
	for {
		select {
		case <-bg.stop:
			bg.e.cancelQuestion(req.ID)
			return
		case <-req.Done:
		case <-deadlineC:
			bg.e.cancelQuestion(req.ID) // answers "cancelled" to a waiting asker
		case <-poll.C:
			bg.e.questionMu.Lock()
			_, open := bg.e.questions[req.ID]
			bg.e.questionMu.Unlock()
			if open {
				continue
			}
		}
		bg.e.questionMu.Lock()
		delete(bg.e.questions, req.ID)
		bg.e.questionMu.Unlock()
		bg.e.removePendingQuestion(bg.sid, req.ID)
		bg.e.syncHub.background(bg.sid, wireEvent{Type: "question_closed", ID: req.ID})
		return
	}
}

func (e *Engine) addPendingQuestion(sid string, q questionWire) {
	e.questionMu.Lock()
	if e.pendingQuestions == nil {
		e.pendingQuestions = map[string]map[string]pendingQuestion{}
	}
	if e.pendingQuestions[sid] == nil {
		e.pendingQuestions[sid] = map[string]pendingQuestion{}
	}
	e.pendingQuestions[sid][q.ID] = pendingQuestion{wire: q, added: time.Now()}
	n := len(e.pendingQuestions[sid])
	e.questionMu.Unlock()
	e.syncHub.backgroundQuestions(sid, n)
}

func (e *Engine) removePendingQuestion(sid, id string) {
	e.questionMu.Lock()
	delete(e.pendingQuestions[sid], id)
	n := len(e.pendingQuestions[sid])
	if n == 0 {
		delete(e.pendingQuestions, sid)
	}
	e.questionMu.Unlock()
	e.syncHub.backgroundQuestions(sid, n)
}

// PendingQuestions lists a conversation's unanswered background questions,
// oldest first.
func (e *Engine) PendingQuestions(sid string) []questionWire {
	e.questionMu.Lock()
	defer e.questionMu.Unlock()
	list := make([]pendingQuestion, 0, len(e.pendingQuestions[sid]))
	for _, q := range e.pendingQuestions[sid] {
		list = append(list, q)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].added.Before(list[j].added) })
	out := make([]questionWire, 0, len(list))
	for _, q := range list {
		out = append(out, q.wire)
	}
	return out
}

// handlePendingQuestions: GET /api/questions?session=<id>.
func (s *Server) handlePendingQuestions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sid := strings.TrimSpace(r.URL.Query().Get("session"))
	if sid == "" {
		http.Error(w, "session is required", http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"questions": s.eng.PendingQuestions(sid)})
}
