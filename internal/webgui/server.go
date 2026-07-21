package webgui

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"supercli/internal/storage/memory"
)

// Server is the web GUI HTTP server. It wraps an Engine (the agent
// runtime) and exposes it over JSON + SSE. Construct it with
// NewServer; build the routing with Handler.
type Server struct {
	eng       *Engine
	uiHandler http.Handler
	appName   string
	startedAt time.Time
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

	folderJobMu     sync.Mutex
	folderJob       *folderIndexJob
	folderJobCancel context.CancelFunc
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
	return &Server{
		eng:         eng,
		allowRemote: allowRemote,
		appName:     "SuperCli",
		codexLogins: make(map[string]*codexLoginState),
		startedAt:   time.Now().UTC(),
	}
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

// handleSessions lists, renames, or deletes conversations in the active
// workspace. Mutations are workspace-scoped by Engine before touching SQLite.
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit := queryInt(r, "limit", 30)
		out, err := s.eng.listSessions(r.Context(), limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, out)
	case http.MethodPatch:
		var body struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.eng.renameSession(body.ID, body.Title); err != nil {
			writeSessionMutationError(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "id": strings.TrimSpace(body.ID), "title": strings.TrimSpace(body.Title)})
	case http.MethodDelete:
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if err := s.eng.deleteSession(id); err != nil {
			writeSessionMutationError(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "id": id})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func writeSessionMutationError(w http.ResponseWriter, err error) {
	if errors.Is(err, errSessionOutsideWorkspace) || errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "session not found in active project", http.StatusNotFound)
		return
	}
	http.Error(w, err.Error(), http.StatusBadRequest)
}

// handleTranscript returns one session's full message list.
func (s *Server) handleTranscript(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	var out any
	var err error
	if r.URL.Query().Has("limit") {
		limit := queryInt(r, "limit", 100)
		if limit > 500 {
			limit = 500
		}
		out, err = s.eng.transcriptPage(r.Context(), id, queryInt(r, "before", 0), limit)
	} else {
		// Keep the original array response for older clients and integrations.
		out, err = s.eng.transcript(r.Context(), id)
	}
	if err != nil {
		if errors.Is(err, errSessionOutsideWorkspace) {
			http.Error(w, "session not found in active project", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, out)
}

// handleMemory lists memory entries; ?scope= filters by scope.
func (s *Server) handleMemory(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		scope := strings.TrimSpace(r.URL.Query().Get("scope"))
		limit := queryInt(r, "limit", 50)
		out, err := s.eng.memoryList(scope, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, out)
	case http.MethodPost:
		var body struct {
			Content string `json:"content"`
			Type    string `json:"type"`
			Target  string `json:"target"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
		content := strings.TrimSpace(body.Content)
		if content == "" {
			http.Error(w, "content is required", http.StatusBadRequest)
			return
		}
		scope := memory.ScopeFact
		switch strings.ToLower(strings.TrimSpace(body.Type)) {
		case "", "fact":
		case "preference":
			scope = memory.ScopePreference
		case "decision":
			scope = memory.ScopeDecision
		case "task-log":
			scope = memory.ScopeTaskLog
		default:
			http.Error(w, "type must be fact, preference, decision or task-log", http.StatusBadRequest)
			return
		}
		var store *memory.Store
		var err error
		target := strings.ToLower(strings.TrimSpace(body.Target))
		if target == "global" {
			store, err = memory.OpenStore(s.eng.DataDir())
		} else {
			target = "project"
			store, err = memory.OpenProjectStore(s.eng.DataDir(), s.eng.Home())
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer store.Close()
		entry := memory.Entry{
			ID:      fmt.Sprintf("mem-ui-%x", time.Now().UnixNano()),
			Scope:   scope,
			Content: content,
			Source:  memory.SourceUser,
		}
		if err := store.Put(entry); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "id": entry.ID, "target": target})
	case http.MethodDelete:
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if id == "" {
			http.Error(w, "id is required", http.StatusBadRequest)
			return
		}
		target := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("target")))
		var stores []*memory.Store
		if target == "" || target == "project" {
			if store, err := memory.OpenProjectStore(s.eng.DataDir(), s.eng.Home()); err == nil {
				stores = append(stores, store)
			}
		}
		if target == "" || target == "global" {
			if store, err := memory.OpenStore(s.eng.DataDir()); err == nil {
				stores = append(stores, store)
			}
		}
		for _, store := range stores {
			_ = store.Delete(id)
			_ = store.Close()
		}
		writeJSON(w, map[string]any{"ok": true, "id": id})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleProjects lists named workspaces (GET) or performs a
// use/add/remove action (POST {action,target,name}). On add with an empty
// target the current sandbox root is registered; name is an optional
// display name for the added project.
func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body struct {
			Action string `json:"action"`
			Target string `json:"target"`
			Name   string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
		if body.Action == "add" && strings.TrimSpace(body.Target) == "" {
			body.Target = s.eng.Home()
		}
		if err := s.eng.projectAction(body.Action, body.Target, body.Name); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	writeJSON(w, map[string]any{
		"projects": s.eng.listProjects(),
		"home":     s.eng.Home(),
	})
}

// handleGoal returns the active goal and its tasks, or applies one local UI
// mutation. The route remains behind the server's local/remote guard.
func (s *Server) handleGoal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		var in goalMutation
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
		out, err := s.eng.mutateGoal(r.Context(), in)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, out)
		return
	}
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
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("session"))
	out, err := s.eng.stats(r.Context(), id)
	if err != nil {
		if errors.Is(err, errSessionOutsideWorkspace) || errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "session not found in active project", http.StatusNotFound)
			return
		}
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

// isLoopbackRemoteAddr validates the actual TCP peer address. Unlike Host it
// cannot be supplied by a remote browser, so secret-returning handlers use it
// in addition to the DNS-rebinding Host check. Malformed and empty values fail
// closed.
func isLoopbackRemoteAddr(remoteAddr string) bool {
	addr, err := netip.ParseAddrPort(strings.TrimSpace(remoteAddr))
	if err != nil {
		return false
	}
	return addr.Addr().Unmap().IsLoopback()
}

// shutdownTimeout bounds graceful shutdown of the HTTP server.
const shutdownTimeout = 3 * time.Second

// Shutdown gracefully stops srv within shutdownTimeout.
func Shutdown(srv *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return srv.Shutdown(ctx)
}
