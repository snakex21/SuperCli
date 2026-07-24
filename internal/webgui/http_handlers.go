package webgui

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"supercli/internal/storage/memory"
)

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
