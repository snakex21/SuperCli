package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Both transports go through Handler's Host/origin/auth and phone guards.
func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	if s.phone != nil && !isLocalRequest(r) {
		active := s.phone.manager.ActiveContext()
		if active == nil {
			http.Error(w, "phone disconnected", http.StatusUnauthorized)
			return
		}
		stop := context.AfterFunc(active, cancel)
		defer stop()
	}
	sub, snapshot, unsubscribe := s.eng.syncHub.subscribe(strings.TrimSpace(r.URL.Query().Get("session")), r.URL.Query().Get("client"))
	defer unsubscribe()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	write := func(frame syncFrame) bool {
		data, _ := json.Marshal(frame)
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Second))
		_, err := fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		return err == nil
	}
	if !write(snapshot) {
		return
	}
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case frame, open := <-sub.events:
			if !open || !write(frame) {
				return
			}
		case <-ticker.C:
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
func (s *Server) handleSyncWebSocket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ws, err := upgradeWebSocket(w, r)
	if err != nil {
		return
	}
	defer ws.conn.Close()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	// A hijacked connection is not closed automatically on request cancellation.
	stopClose := context.AfterFunc(ctx, func() { ws.conn.Close() })
	defer stopClose()
	if s.phone != nil && !isLocalRequest(r) {
		active := s.phone.manager.ActiveContext()
		if active == nil {
			return
		}
		stop := context.AfterFunc(active, cancel)
		defer stop()
	}
	go func() { _, _ = ws.readText(); cancel() }()
	sub, snapshot, unsubscribe := s.eng.syncHub.subscribe(strings.TrimSpace(r.URL.Query().Get("session")), r.URL.Query().Get("client"))
	defer unsubscribe()
	write := func(frame syncFrame) bool {
		data, _ := json.Marshal(frame)
		return ws.write(1, []byte(fmt.Sprintf("data: %s\n\n", data))) == nil
	}
	if !write(snapshot) {
		return
	}
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case frame, open := <-sub.events:
			if !open || !write(frame) {
				return
			}
		case <-ticker.C:
			if ws.write(9, nil) != nil {
				return
			}
		}
	}
}
func (s *Server) handleSyncStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		SessionID string `json:"session_id"`
		TurnID    string `json:"turn_id"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !s.eng.syncHub.stop(body.SessionID, body.TurnID) {
		// A sender can press Stop before the session/run_started frame arrives.
		s.chatCompletions.Lock()
		run := s.chatCompletions.runs[body.TurnID]
		stopped := false
		if run != nil && run.cancel != nil {
			select {
			case <-run.done:
			default:
				run.cancel()
				stopped = true
			}
		}
		s.chatCompletions.Unlock()
		if !stopped {
			http.Error(w, "run is no longer active", http.StatusGone)
			return
		}
	}
	writeJSON(w, map[string]bool{"ok": true})
}
