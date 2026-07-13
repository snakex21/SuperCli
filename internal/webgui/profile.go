package webgui

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	llmprompt "supercli/internal/llm/prompt"
)

func (s *Server) handlePromptProfile(w http.ResponseWriter, r *http.Request) {
	model := s.eng.ModelName()
	family := llmprompt.ProfileFamily(model)
	path := filepath.Join(s.eng.Home(), ".supercli", "prompts", family+".md")
	if r.Method == http.MethodGet {
		b, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]any{"model": model, "family": family, "path": path, "content": string(b)})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if len(body.Content) > 4096 {
		http.Error(w, "profile exceeds 4096 byte limit", 400)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(body.Content)), 0644); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "path": path})
}

func (s *Server) handleScratchpad(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	root := filepath.Join(s.eng.Home(), ".supercli", "scratchpad")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		writeJSON(w, map[string]any{"path": root, "notes": []string{}})
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	notes := []string{}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			notes = append(notes, strings.TrimSuffix(e.Name(), ".md"))
		}
	}
	writeJSON(w, map[string]any{"path": root, "notes": notes})
}
