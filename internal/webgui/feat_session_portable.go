package webgui

import (
	"bytes"
	"context"
	"errors"
	"mime"
	"net/http"
	"os"
	"strings"
	"time"

	"supercli/internal/storage/session"
)

// portableImportOverhead leaves room for multipart headers/boundaries around
// a session file that is itself capped at session.MaxPortableBytes.
const portableImportOverhead = 1 << 20

// handleSessionExport downloads one conversation as a portable JSON file.
func (s *Server) handleSessionExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store, private")
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	store, err := s.eng.sessionStore()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	meta, err := store.Get(id)
	if err != nil || !sameSessionWorkspace(meta.Cwd, s.eng.Home()) {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	// Buffer so a mid-export failure still yields a proper error status
	// instead of a truncated attachment.
	var buf bytes.Buffer
	if _, err := store.WritePortable(ctx, id, &buf); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": session.PortableFilename(meta)}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(buf.Bytes())
}

// handleSessionImport adds one conversation from a portable file as a NEW
// session; existing sessions are never replaced.
func (s *Server) handleSessionImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store, private")
	r.Body = http.MaxBytesReader(w, r.Body, session.MaxPortableBytes+portableImportOverhead)
	upload, cleanup, err := stageMultipartUpload(r, s.eng.DataDir(), "session-import-*", session.MaxPortableBytes, "session")
	if err != nil {
		status := uploadErrorStatus(err)
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) || errors.Is(err, session.ErrPortableTooLarge) || strings.Contains(err.Error(), "exceeds the") {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, "invalid session upload: "+err.Error(), status)
		return
	}
	defer cleanup()
	f, err := os.Open(upload.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()
	store, err := s.eng.sessionStore()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	sess, err := store.ImportPortableReader(ctx, f, session.PortableImportOptions{Cwd: s.eng.Home()})
	switch {
	case errors.Is(err, session.ErrPortableTooLarge):
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		return
	case errors.Is(err, session.ErrPortableInvalid):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.eng.syncHub.changed()
	writeJSON(w, map[string]any{"ok": true, "id": sess.ID, "title": sess.Title})
}
