package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Workspace is the richer "named project" model layered on top of the
// path→memory-key map in projects.go. A Project is a named workspace: a
// working directory (which becomes the agent's sandbox root) plus an
// optional preferred model/provider. Exactly one project may be Active;
// the active project's path drives the sandbox root and its model/provider
// seed the runtime config at startup.
//
// This is stored separately from projects.json (the path→memory.db map) so
// the existing per-project memory keying keeps working untouched — there is
// still one source of truth for each concern: projects.json for memory
// storage keys, workspace.json for the named-workspace list + active pointer.
type Workspace struct {
	Projects []Project `json:"projects"`
	// Active is the Path of the active project, or "" for none.
	Active string `json:"active,omitempty"`
}

// Project is one named workspace.
type Project struct {
	Name string `json:"name"`
	// Path is the absolute working directory / sandbox root.
	Path string `json:"path"`
	// Model and Provider are the optional per-project preferences. When
	// set and no --model/--provider flag or env override is present, they
	// seed the runtime config at startup.
	Model    string `json:"model,omitempty"`
	Provider string `json:"provider,omitempty"`
}

// workspaceFile is the on-disk path of the named-workspace list.
func workspaceFile(home string) string {
	return filepath.Join(home, "workspace.json")
}

// LoadWorkspace reads <home>/workspace.json. Like the rest of the memory
// layer it is deliberately tolerant: a missing or malformed file yields an
// empty workspace rather than blocking startup.
func LoadWorkspace(home string) *Workspace {
	w := &Workspace{}
	raw, err := os.ReadFile(workspaceFile(home))
	if err != nil {
		return w
	}
	if err := json.Unmarshal(raw, w); err != nil {
		if repaired, ok := repairJSONObject(string(raw)); ok {
			_ = json.Unmarshal([]byte(repaired), w)
		}
	}
	// Drop an Active pointer that no longer matches any project.
	if w.Active != "" {
		if _, ok := w.find(w.Active); !ok {
			w.Active = ""
		}
	}
	return w
}

// SaveWorkspace writes the workspace back. Best effort — memory is non-fatal.
func SaveWorkspace(home string, w *Workspace) error {
	raw, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(workspaceFile(home), raw, 0o644)
}

// find locates a project by absolute path, name, or basename
// (case-insensitive), returning its index. The resolution order mirrors
// resolveProject in internal/app/projects.go so users need not know the
// canonical form.
func (w *Workspace) find(target string) (int, bool) {
	target = strings.TrimSpace(strings.Trim(target, `"`))
	if target == "" {
		return -1, false
	}
	// 1. Exact path.
	for i := range w.Projects {
		if w.Projects[i].Path == target {
			return i, true
		}
	}
	// 2. Exact name (case-insensitive).
	for i := range w.Projects {
		if strings.EqualFold(w.Projects[i].Name, target) {
			return i, true
		}
	}
	// 3. Basename of the path (case-insensitive).
	tb := strings.ToLower(filepath.Base(target))
	for i := range w.Projects {
		if strings.ToLower(filepath.Base(w.Projects[i].Path)) == tb {
			return i, true
		}
	}
	return -1, false
}

// Get returns the project matching target (path, name, or basename).
func (w *Workspace) Get(target string) (Project, bool) {
	if i, ok := w.find(target); ok {
		return w.Projects[i], true
	}
	return Project{}, false
}

// Upsert adds p, or updates the existing project with the same path.
// Empty Model/Provider on an update leave the stored values intact so a
// bare re-`add` never wipes a previously set preference.
func (w *Workspace) Upsert(p Project) {
	if p.Name == "" {
		p.Name = filepath.Base(p.Path)
	}
	if i, ok := w.find(p.Path); ok {
		w.Projects[i].Name = p.Name
		if p.Model != "" {
			w.Projects[i].Model = p.Model
		}
		if p.Provider != "" {
			w.Projects[i].Provider = p.Provider
		}
		return
	}
	w.Projects = append(w.Projects, p)
}

// Remove deletes the project matching target and clears Active if it
// pointed at that project. Returns the removed project and whether a
// match was found.
func (w *Workspace) Remove(target string) (Project, bool) {
	i, ok := w.find(target)
	if !ok {
		return Project{}, false
	}
	removed := w.Projects[i]
	w.Projects = append(w.Projects[:i], w.Projects[i+1:]...)
	if w.Active == removed.Path {
		w.Active = ""
	}
	return removed, true
}

// SetActive marks the project matching target active. Returns the resolved
// project and whether a match was found.
func (w *Workspace) SetActive(target string) (Project, bool) {
	i, ok := w.find(target)
	if !ok {
		return Project{}, false
	}
	w.Active = w.Projects[i].Path
	return w.Projects[i], true
}

// ActiveProject returns the active project, if one is set and still exists.
func (w *Workspace) ActiveProject() (Project, bool) {
	if w.Active == "" {
		return Project{}, false
	}
	return w.Get(w.Active)
}
