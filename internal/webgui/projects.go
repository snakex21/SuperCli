package webgui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

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
	out := make([]projectView, 0, len(ws.Projects))
	for _, p := range ws.Projects {
		out = append(out, projectView{
			Name:   p.Name,
			Path:   p.Path,
			Key:    memory.ProjectKey(p.Path),
			Model:  p.Model,
			Active: p.Path == ws.Active,
			Cwd:    p.Path == e.home,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// projectAction performs use/add/remove on the workspace store. Selecting a
// project (use) persists the active pointer; the sandbox root and model only
// take effect on the next launch — the running engine's home never changes
// mid-session, matching the CLI's behaviour.
func (e *Engine) projectAction(action, target string) error {
	ws := e.loadWorkspaceMerged()
	switch action {
	case "use", "select":
		if _, ok := ws.SetActive(target); !ok {
			return fmt.Errorf("no project matches %q", target)
		}
	case "add":
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
		ws.Upsert(memory.Project{Name: filepath.Base(abs), Path: abs})
		// Keep the legacy path→key map in sync so per-project memory works.
		m := memory.LoadProjectsMap(e.dataDir)
		m[abs] = memory.ProjectKey(abs)
		_ = memory.SaveProjectsMap(e.dataDir, m)
	case "remove", "delete":
		if _, ok := ws.Remove(target); !ok {
			return fmt.Errorf("no project matches %q", target)
		}
	default:
		return fmt.Errorf("unknown action %q", action)
	}
	return memory.SaveWorkspace(e.dataDir, ws)
}
