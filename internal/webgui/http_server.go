package webgui

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
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
	// sessionToken is generated once per process when remote access is enabled.
	// It gates every API request; the locally opened UI receives it through a
	// loopback-only cookie bootstrap, so it never appears in a URL or log.
	sessionToken string

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

// NewServer returns a Server backed by eng. allowRemote should stay false
// unless the operator explicitly wants token-protected network exposure.
func NewServer(eng *Engine, allowRemote bool) *Server {
	var sessionToken string
	if allowRemote {
		sessionToken = newSessionToken()
	}
	return &Server{
		eng:          eng,
		allowRemote:  allowRemote,
		sessionToken: sessionToken,
		appName:      "SuperCli",
		codexLogins:  make(map[string]*codexLoginState),
		startedAt:    time.Now().UTC(),
	}
}

func newSessionToken() string {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// An explicitly network-exposed server must never start without strong
		// authentication merely because the system RNG failed.
		panic("webgui: cannot generate remote session token: " + err.Error())
	}
	return hex.EncodeToString(raw[:])
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
	providerType, chatReady := s.eng.ProviderStatus()
	writeJSON(w, map[string]any{
		"ok":            true,
		"chat_ready":    chatReady,
		"provider_type": providerType,
		"model":         s.eng.ModelName(),
		"home":          s.eng.Home(),
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
