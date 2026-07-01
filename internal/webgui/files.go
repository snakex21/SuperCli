package webgui

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// handleFiles lists files in a directory (shallow, like a file tree).
func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	dir := strings.TrimSpace(r.URL.Query().Get("dir"))
	if dir == "" {
		dir = s.eng.Home()
	}
	// Security: only allow paths within home
	abs, err := filepath.Abs(dir)
	if err != nil || !strings.HasPrefix(filepath.Clean(abs), filepath.Clean(s.eng.Home())) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	entries, err := os.ReadDir(abs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type fileEntry struct {
		Name  string `json:"name"`
		IsDir bool   `json:"is_dir"`
		Size  int64  `json:"size"`
	}
	out := make([]fileEntry, 0, len(entries))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue // skip hidden
		}
		info, _ := e.Info()
		sz := int64(0)
		if info != nil {
			sz = info.Size()
		}
		out = append(out, fileEntry{Name: e.Name(), IsDir: e.IsDir(), Size: sz})
	}
	writeJSON(w, map[string]any{"dir": abs, "files": out})
}

// handleFileRead reads a file and returns its content.
func (s *Server) handleFileRead(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	abs, err := filepath.Abs(path)
	if err != nil || !strings.HasPrefix(filepath.Clean(abs), filepath.Clean(s.eng.Home())) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"path": abs, "content": string(data)})
}

// handleFileWrite writes content to a file (create or overwrite).
func (s *Server) handleFileWrite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	abs, err := filepath.Abs(req.Path)
	if err != nil || !strings.HasPrefix(filepath.Clean(abs), filepath.Clean(s.eng.Home())) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := os.WriteFile(abs, []byte(req.Content), 0o644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "path": abs})
}
