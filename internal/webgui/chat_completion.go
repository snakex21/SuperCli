package webgui

import (
	"errors"
	"net/http"
	"strings"
	"sync"
)

const chatCompletionLimit = 128

// chatCompletion survives a disconnected SSE response. The separate request
// waits on done exactly once, after the canceled run has finished saving.
type chatCompletion struct {
	done      chan struct{}
	sessionID string
	accepted  bool
}
type chatCompletions struct {
	sync.Mutex
	runs map[string]*chatCompletion
}

func (s *Server) registerChatCompletion(id string) (*chatCompletion, error) {
	if id == "" {
		return nil, nil
	} // older clients do not request receipts
	if len(id) > 128 {
		return nil, errors.New("invalid turn id")
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return nil, errors.New("invalid turn id")
		}
	}
	s.chatCompletions.Lock()
	defer s.chatCompletions.Unlock()
	if s.chatCompletions.runs == nil {
		s.chatCompletions.runs = make(map[string]*chatCompletion)
	}
	if _, exists := s.chatCompletions.runs[id]; exists {
		return nil, errors.New("turn id already used")
	}
	// Reclaim only completed entries; never lose a waiter for an active run.
	if len(s.chatCompletions.runs) >= chatCompletionLimit {
		for key, run := range s.chatCompletions.runs {
			select {
			case <-run.done:
				delete(s.chatCompletions.runs, key)
			default:
			}
		}
	}
	if len(s.chatCompletions.runs) >= chatCompletionLimit {
		return nil, errors.New("too many active turns")
	}
	run := &chatCompletion{done: make(chan struct{})}
	s.chatCompletions.runs[id] = run
	return run, nil
}
func (s *Server) handleChatCompletion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	s.chatCompletions.Lock()
	run := s.chatCompletions.runs[id]
	s.chatCompletions.Unlock()
	if run == nil {
		http.Error(w, "turn not found", http.StatusNotFound)
		return
	}
	select {
	case <-r.Context().Done():
		return
	case <-run.done:
	}
	// The close publishes sessionID, including a freshly created conversation
	// whose initial SSE event was lost when the client pressed Stop.
	writeJSON(w, struct {
		SessionID string `json:"session_id"`
		Accepted  bool   `json:"accepted"`
	}{run.sessionID, run.accepted})
	s.chatCompletions.Lock()
	if s.chatCompletions.runs[id] == run {
		delete(s.chatCompletions.runs, id)
	}
	s.chatCompletions.Unlock()
}
