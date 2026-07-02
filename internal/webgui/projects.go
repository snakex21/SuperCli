package webgui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"supercli/internal/storage/memory"
)

// projectView is the JSON shape the /api/projects endpoint returns for one
// named workspace.
type projectView struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Key    string `json:"key"`
	Model  string `json:"model,omitempty"`
	Active bool   `json:"active"`
	Cwd    bool   `json:"cwd"`
}

// loadWorkspaceMerged loads the named-workspace store, lazily migrating any
// legacy projects.json path→key entries so nothing a user registered before
// the workspace feature disappears. Mirrors app.loadWorkspace so the web GUI
// and the TUI/CLI share one on-disk source of truth.
func (e *Engine) loadWorkspaceMerged() *memory.Workspace {
	ws := memory.LoadWorkspace(e.dataDir)
	have := map[string]bool{}
	for _, p := range ws.Projects {
		have[p.Path] = true
	}
	changed := false
	for path := range memory.LoadProjectsMap(e.dataDir) {
		if !have[path] {
			ws.Upsert(memory.Project{Name: filepath.Base(path), Path: path})
			changed = true
		}
	}
	if changed {
		_ = memory.SaveWorkspace(e.dataDir, ws)
	}
	return ws
}

// listProjects returns the named workspaces, sorted by path, with the active
// one and the current sandbox root flagged.
func (e *Engine) listProjects() []projectView {
	ws := e.loadWorkspaceMerged()
	home := e.Home()
	out := make([]projectView, 0, len(ws.Projects))
	for _, p := range ws.Projects {
		out = append(out, projectView{
			Name:   p.Name,
			Path:   p.Path,
			Key:    memory.ProjectKey(p.Path),
			Model:  p.Model,
			Active: p.Path == ws.Active,
			Cwd:    p.Path == home,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// projectAction performs use/add/remove on the workspace store. Storage mirrors
// the CLI /projects command (projects.json + workspace.json + project memory
// store), while the web GUI additionally hot-swaps Engine.home so future web
// requests use the selected sandbox immediately. name is an optional display
// name for "add"; when empty the directory basename is used.
func (e *Engine) projectAction(action, target, name string) error {
	ws := e.loadWorkspaceMerged()
	switch action {
	case "use", "select":
		p, ok := ws.SetActive(target)
		if !ok {
			return fmt.Errorf("no project matches %q", target)
		}
		fi, err := os.Stat(p.Path)
		if err != nil {
			return fmt.Errorf("%q does not exist", p.Path)
		}
		if !fi.IsDir() {
			return fmt.Errorf("%q is not a directory", p.Path)
		}
		if err := memory.SaveWorkspace(e.dataDir, ws); err != nil {
			return err
		}
		e.setHome(p.Path)
		return nil
	case "add":
		if strings.TrimSpace(target) == "" {
			target = e.Home()
		}
		abs, err := filepath.Abs(target)
		if err != nil {
			return fmt.Errorf("cannot resolve %q: %w", target, err)
		}
		fi, err := os.Stat(abs)
		if err != nil {
			return fmt.Errorf("%q does not exist", abs)
		}
		if !fi.IsDir() {
			return fmt.Errorf("%q is not a directory", abs)
		}
		key := memory.ProjectKey(abs)
		m := memory.LoadProjectsMap(e.dataDir)
		m[abs] = key
		if err := memory.SaveProjectsMap(e.dataDir, m); err != nil {
			return fmt.Errorf("save project map: %w", err)
		}
		name = strings.TrimSpace(name)
		if name == "" {
			name = filepath.Base(abs)
		}
		ws.Upsert(memory.Project{Name: name, Path: abs})
		ws.SetActive(abs)
		if err := memory.SaveWorkspace(e.dataDir, ws); err != nil {
			return fmt.Errorf("save workspace: %w", err)
		}
		store, err := memory.OpenProjectStore(e.dataDir, abs)
		if err != nil {
			return fmt.Errorf("init project store: %w", err)
		}
		_ = store.Close()
		e.setHome(abs)
		return nil
	case "remove", "delete":
		removed, ok := ws.Remove(target)
		if !ok {
			return fmt.Errorf("no project matches %q", target)
		}
		// Drop the legacy projects.json entry too, mirroring the CLI's
		// /projects remove — otherwise loadWorkspaceMerged re-imports the
		// path on the next list and the project appears to come back.
		// Per-project memory.db is intentionally kept on disk so that
		// re-registering the same project later restores prior memory.
		m := memory.LoadProjectsMap(e.dataDir)
		if _, mapped := m[removed.Path]; mapped {
			delete(m, removed.Path)
			if err := memory.SaveProjectsMap(e.dataDir, m); err != nil {
				return fmt.Errorf("save project map: %w", err)
			}
		}
	default:
		return fmt.Errorf("unknown action %q", action)
	}
	return memory.SaveWorkspace(e.dataDir, ws)
}

// handleFolderPicker opens a native Windows folder browser dialog via
// PowerShell and returns the selected path. The request blocks until the
// user picks a folder or cancels.
func (s *Server) handleFolderPicker(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ps := `$ErrorActionPreference = 'Stop'; [Console]::OutputEncoding = [System.Text.Encoding]::UTF8; Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.Application]::EnableVisualStyles(); $owner = New-Object System.Windows.Forms.Form; $owner.StartPosition = 'CenterScreen'; $owner.ShowInTaskbar = $false; $owner.TopMost = $true; $owner.Width = 1; $owner.Height = 1; $owner.Opacity = 0; $owner.Show(); $owner.Activate(); $d = New-Object System.Windows.Forms.FolderBrowserDialog; $d.Description = 'Select project folder'; $d.ShowNewFolderButton = $true; $r = $d.ShowDialog($owner); if ($r -eq [System.Windows.Forms.DialogResult]::OK) { [Console]::Write($d.SelectedPath) }; $owner.Close(); $owner.Dispose()`
	cmd := exec.Command("powershell", "-NoProfile", "-STA", "-ExecutionPolicy", "Bypass", "-Command", ps)
	out, err := cmd.CombinedOutput()
	if err != nil {
		http.Error(w, "dialog failed: "+string(out), http.StatusInternalServerError)
		return
	}
	raw := strings.TrimSpace(string(out))
	// Strip BOM if present
	raw = strings.TrimPrefix(raw, "\xEF\xBB\xBF")
	// Take only the last non-empty line (PowerShell may output extra lines)
	lines := strings.Split(raw, "\n")
	path := ""
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l != "" {
			path = l
			break
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"path": path})
}
