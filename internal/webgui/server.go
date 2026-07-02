package webgui

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Server is the web GUI HTTP server. It wraps an Engine (the agent
// runtime) and exposes it over JSON + SSE. Construct it with
// NewServer; build the routing with Handler.
type Server struct {
	eng *Engine
	// allowRemote disables the loopback-only guard. Off by default:
	// the GUI is a local single-user app. The separate binary may
	// flip it on for explicit, opt-in network exposure.
	allowRemote bool

	// codexLoginMu guards codexLogins, the in-memory map of
	// in-progress (or recently finished) Codex account logins.
	// The Login() flow is asynchronous: it opens a browser and
	// waits for the OAuth callback for up to a few minutes. The
	// HTTP handler therefore returns immediately and the frontend
	// polls /api/codex/accounts to detect the LoggedIn flip.
	codexLoginMu sync.Mutex
	codexLogins  map[string]*codexLoginState
}

// codexLoginState is the per-account (by label) tracking record for
// an asynchronous Login() call. err is nil on success, set on
// failure, and reset (along with inProgress/done) the next time a
// login for this label is started.
type codexLoginState struct {
	inProgress bool      // true while Login() is running
	done       time.Time // when Login() finished (zero value while inProgress)
	err        error     // nil on success
}

// NewServer returns a Server backed by eng. allowRemote should stay
// false unless the operator explicitly wants network exposure (which
// then needs its own auth — not provided here).
func NewServer(eng *Engine, allowRemote bool) *Server {
	return &Server{eng: eng, allowRemote: allowRemote, codexLogins: make(map[string]*codexLoginState)}
}

// codexLoginInProgress returns the current state of an async login
// for the given label. Safe for concurrent use.
func (s *Server) codexLoginInProgress(label string) bool {
	s.codexLoginMu.Lock()
	defer s.codexLoginMu.Unlock()
	st := s.codexLogins[label]
	return st != nil && st.inProgress
}

// codexLoginErr returns the last error (or nil) of a finished login
// for the given label. The state is kept around after completion so
// the frontend can fetch it on the next poll.
func (s *Server) codexLoginErr(label string) error {
	s.codexLoginMu.Lock()
	defer s.codexLoginMu.Unlock()
	st := s.codexLogins[label]
	if st == nil || st.inProgress {
		return nil
	}
	return st.err
}

// startCodexLogin marks a login as in progress for label and returns
// a completion callback that records the final result. If a login is
// already in progress for this label, the second boolean return is
// false and the caller should not start a new one.
func (s *Server) startCodexLogin(label string) (func(error), bool) {
	s.codexLoginMu.Lock()
	defer s.codexLoginMu.Unlock()
	if st := s.codexLogins[label]; st != nil && st.inProgress {
		return nil, false
	}
	st := &codexLoginState{inProgress: true}
	s.codexLogins[label] = st
	return func(err error) {
		s.codexLoginMu.Lock()
		st.inProgress = false
		st.done = time.Now()
		st.err = err
		s.codexLoginMu.Unlock()
	}, true
}

// writeJSON encodes v as JSON with a 200 status. Encode failures are
// logged via the error body; they cannot be recovered mid-response.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, "encode: "+err.Error(), http.StatusInternalServerError)
	}
}

// handleHealth reports liveness plus the active model so the front-end
// can show connection status on load.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"ok":    true,
		"model": s.eng.ModelName(),
		"home":  s.eng.Home(),
	})
}

// handleSessions lists recent sessions for the history panel.
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 30)
	out, err := s.eng.listSessions(r.Context(), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, out)
}

// handleTranscript returns one session's full message list.
func (s *Server) handleTranscript(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	out, err := s.eng.transcript(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, out)
}

// handleMemory lists memory entries; ?scope= filters by scope.
func (s *Server) handleMemory(w http.ResponseWriter, r *http.Request) {
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	limit := queryInt(r, "limit", 50)
	out, err := s.eng.memoryList(scope, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, out)
}

// handleProjects lists named workspaces (GET) or performs a
// use/add/remove action (POST {action,target}). On add with an empty
// target the current sandbox root is registered.
func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body struct {
			Action string `json:"action"`
			Target string `json:"target"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
		if body.Action == "add" && strings.TrimSpace(body.Target) == "" {
			body.Target = s.eng.Home()
		}
		if err := s.eng.projectAction(body.Action, body.Target); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	writeJSON(w, map[string]any{
		"projects": s.eng.listProjects(),
		"home":     s.eng.Home(),
	})
}

// handleGoal returns the active goal and its tasks, or null.
func (s *Server) handleGoal(w http.ResponseWriter, r *http.Request) {
	out, err := s.eng.activeGoal(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, out)
}

// handleStats returns the cost/credit dashboard; ?session= scopes the
// per-session token total.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("session"))
	out, err := s.eng.stats(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, out)
}

// queryInt reads an integer query parameter, returning def when it is
// absent or malformed.
func queryInt(r *http.Request, key string, def int) int {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// isLoopbackHost reports whether host (Host header, maybe with port)
// refers to a loopback address or the conventional localhost name.
func isLoopbackHost(host string) bool {
	h := host
	if hostOnly, _, err := net.SplitHostPort(host); err == nil {
		h = hostOnly
	}
	h = strings.TrimSpace(strings.ToLower(h))
	if h == "localhost" || h == "" {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// shutdownTimeout bounds graceful shutdown of the HTTP server.
const shutdownTimeout = 3 * time.Second

// Shutdown gracefully stops srv within shutdownTimeout.
func Shutdown(srv *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return srv.Shutdown(ctx)
}
