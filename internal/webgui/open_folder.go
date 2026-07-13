package webgui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// handleOpenFolder opens a workspace directory in the operating system's file
// manager. Only paths inside the active workspace are accepted. The endpoint
// is invoked by an explicit user click and never by the model.
func (s *Server) handleOpenFolder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	path, err := workspaceFolder(s.eng.Home(), body.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	// The two workflow folders may not exist before their first write. Creating
	// an empty directory here is harmless and lets Explorer open immediately.
	if err := os.MkdirAll(path, 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := openSystemFolder(path); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "path": path})
}

func workspaceFolder(home, requested string) (string, error) {
	homeAbs, err := filepath.Abs(home)
	if err != nil {
		return "", err
	}
	requested = strings.TrimSpace(requested)
	if requested == "" {
		requested = homeAbs
	} else if !filepath.IsAbs(requested) {
		requested = filepath.Join(homeAbs, requested)
	}
	abs, err := filepath.Abs(requested)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(homeAbs, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("folder is outside the active workspace")
	}
	return filepath.Clean(abs), nil
}

func openSystemFolder(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer.exe", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open folder: %w", err)
	}
	return cmd.Process.Release()
}
