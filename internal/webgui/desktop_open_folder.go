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

// handleOpenFolder opens a directory in the OS file manager. User-click only.
// Allowed: active workspace, SuperCli data dir (and children), or any existing
// absolute directory the user previously chose as an OCR export folder.
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
	path, err := s.resolveOpenablePath(body.Path, true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
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

// handleOpenPath opens a file (e.g. DOCX in Word) or folder. User-click only.
func (s *Server) handleOpenPath(w http.ResponseWriter, r *http.Request) {
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
	path, err := s.resolveOpenablePath(body.Path, false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if info.IsDir() {
		if err := openSystemFolder(path); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else if err := openSystemFile(path); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "path": path})
}

func (s *Server) resolveOpenablePath(requested string, preferDir bool) (string, error) {
	requested = strings.TrimSpace(requested)
	homeAbs, err := filepath.Abs(s.eng.Home())
	if err != nil {
		return "", err
	}
	dataAbs, err := filepath.Abs(s.eng.DataDir())
	if err != nil {
		return "", err
	}
	if requested == "" {
		if preferDir {
			return homeAbs, nil
		}
		return "", fmt.Errorf("missing path")
	}
	if !filepath.IsAbs(requested) {
		// Relative paths resolve against workspace first.
		requested = filepath.Join(homeAbs, requested)
	}
	abs, err := filepath.Abs(requested)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if underPath(homeAbs, abs) || underPath(dataAbs, abs) {
		return abs, nil
	}
	// Absolute path outside workspace/data: allow only if it already exists
	// (user-chosen export folder / saved OCR file). Never create outside roots here.
	if info, statErr := os.Stat(abs); statErr == nil {
		if preferDir && !info.IsDir() {
			return filepath.Dir(abs), nil
		}
		return abs, nil
	}
	return "", fmt.Errorf("path is outside the workspace and does not exist")
}

func underPath(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func workspaceFolder(home, requested string) (string, error) {
	// Kept for older call sites / tests.
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
	if !underPath(homeAbs, abs) {
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

func openSystemFile(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// start opens with the default app (Word for .docx).
		cmd = exec.Command("cmd", "/c", "start", "", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	return cmd.Process.Release()
}
