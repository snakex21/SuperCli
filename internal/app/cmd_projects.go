// Package app: slash-command layer for managing the per-project
// memory map. The actual storage lives in
// internal/storage/memory/projects.go; this file only wires it to
// the user-facing /projects command and (separately) to the TUI
// menu — there is one source of truth for the path→key map.
package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"supercli/internal/storage/memory"
)

// projectsCommand implements the /projects slash command:
//
//	/projects                 — list projects (active marked with ★, cwd with →)
//	/projects list            — same as above
//	/projects add [path]      — register a project (default = cwd)
//	/projects use <X>         — make a project active (sandbox root next launch)
//	/projects remove <X>      — unregister by path, name, or basename
//	/projects info <X>        — show project memory details
//	/projects help            — usage
//
// `X` in use/remove/info can be the full path, the project name,
// the project key ("name-8hex"), or the bare basename of the
// directory — the resolver tries each in turn so the user does not
// need to know the canonical form.
func projectsCommand(_ context.Context, args, dataDir string) (string, error) {
	args = strings.TrimSpace(args)
	cmd, rest := splitCmd(args)

	switch cmd {
	case "", "list":
		return projectsList(dataDir), nil
	case "add":
		return projectsAdd(dataDir, rest)
	case "use", "select", "switch", "activate":
		return projectsUse(dataDir, rest)
	case "remove", "rm", "delete":
		return projectsRemove(dataDir, rest)
	case "info", "show":
		return projectsInfo(dataDir, rest), nil
	case "help", "?":
		return projectsHelp(), nil
	default:
		return projectsHelp(), nil
	}
}

// loadWorkspace loads the named-workspace store, lazily migrating any
// projects that only exist in the legacy projects.json path→key map so a
// user who registered projects before this feature still sees them.
func loadWorkspace(dataDir string) *memory.Workspace {
	ws := memory.LoadWorkspace(dataDir)
	changed := false
	for path := range memory.LoadProjectsMap(dataDir) {
		if _, ok := ws.Get(path); !ok {
			ws.Upsert(memory.Project{Name: filepath.Base(path), Path: path})
			changed = true
		}
	}
	if changed {
		_ = memory.SaveWorkspace(dataDir, ws)
	}
	return ws
}

// projectsUse marks a project active. Because the sandbox root is a
// startup decision, the change takes effect on the next launch (or the
// next fresh session started from a restart) — we say so plainly instead
// of pretending it hot-swaps the running agent's working directory.
func projectsUse(dataDir, target string) (string, error) {
	if target == "" {
		return "", fmt.Errorf("usage: /projects use <path|name|basename>")
	}
	ws := loadWorkspace(dataDir)
	p, ok := ws.SetActive(target)
	if !ok {
		return "", fmt.Errorf("projects: no project matches %q (use /projects add first)", target)
	}
	if err := memory.SaveWorkspace(dataDir, ws); err != nil {
		return "", fmt.Errorf("projects: save workspace: %w", err)
	}
	msg := fmt.Sprintf("Active project → %s\n  path: %s", p.Name, p.Path)
	if p.Model != "" {
		msg += "\n  model: " + p.Model
	}
	msg += "\n\nSandbox root and model apply on the next launch (restart SuperCli)."
	return msg, nil
}

// splitCmd peels the first word off args, returning the command
// verb and whatever follows (already trimmed). Empty args yield
// ("", "") so the caller can route to the default subcommand.
func splitCmd(args string) (string, string) {
	args = strings.TrimSpace(args)
	if args == "" {
		return "", ""
	}
	i := strings.IndexByte(args, ' ')
	if i < 0 {
		return args, ""
	}
	return args[:i], strings.TrimSpace(args[i+1:])
}

// resolveProject finds (path, key) for a target that may be the
// full path, the project key, or a basename. Returns ok=false if
// nothing matches.
func resolveProject(m map[string]string, target string) (path, key string, ok bool) {
	// 1. Exact path
	if k, hit := m[target]; hit {
		return target, k, true
	}
	// 2. Project key ("name-8hex")
	for p, k := range m {
		if k == target {
			return p, k, true
		}
	}
	// 3. Basename (case-insensitive)
	tb := strings.ToLower(filepath.Base(strings.Trim(target, `"`)))
	for p, k := range m {
		if strings.ToLower(filepath.Base(p)) == tb {
			return p, k, true
		}
	}
	return "", "", false
}

func projectsList(dataDir string) string {
	ws := loadWorkspace(dataDir)
	if len(ws.Projects) == 0 {
		return "No projects registered. Use /projects add [path] to register one."
	}
	projs := append([]memory.Project(nil), ws.Projects...)
	sort.Slice(projs, func(i, j int) bool { return projs[i].Path < projs[j].Path })

	cwd, _ := os.Getwd()
	var b strings.Builder
	fmt.Fprintf(&b, "Projects (%d)  ★ = active · → = current directory:\n", len(projs))
	for _, p := range projs {
		marker := "  "
		if p.Path == ws.Active {
			marker = "★ "
		} else if p.Path == cwd {
			marker = "→ "
		}
		key := memory.ProjectKey(p.Path)
		dbPath := filepath.Join(dataDir, "projects", key, "memory.db")
		size := "(no memory yet)"
		if fi, err := os.Stat(dbPath); err == nil {
			size = fmt.Sprintf("%.1f KB", float64(fi.Size())/1024)
		}
		pref := ""
		if p.Model != "" {
			pref = "  model=" + p.Model
		}
		fmt.Fprintf(&b, "%s%s  [%s]  %s%s\n", marker, p.Name, key, size, pref)
		fmt.Fprintf(&b, "    %s\n", p.Path)
	}
	return strings.TrimRight(b.String(), "\n")
}

func projectsAdd(dataDir, path string) (string, error) {
	if path == "" {
		var err error
		path, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("projects: getwd: %w", err)
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("projects: cannot resolve %q: %w", path, err)
	}
	// Reject non-existing / non-directory entries — registering a
	// typo'd path silently would mean memory writes go nowhere.
	fi, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("projects: %q does not exist: %w", abs, err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("projects: %q is not a directory", abs)
	}

	key := memory.ProjectKey(abs)
	m := memory.LoadProjectsMap(dataDir)
	alreadyMapped := false
	if existing, ok := m[abs]; ok && existing == key {
		alreadyMapped = true
	}
	m[abs] = key
	if err := memory.SaveProjectsMap(dataDir, m); err != nil {
		return "", fmt.Errorf("projects: save map: %w", err)
	}
	// Register the named workspace too (source of truth for the project
	// list + active pointer). Upsert never wipes an existing model/provider.
	ws := loadWorkspace(dataDir)
	ws.Upsert(memory.Project{Name: filepath.Base(abs), Path: abs})
	if err := memory.SaveWorkspace(dataDir, ws); err != nil {
		return "", fmt.Errorf("projects: save workspace: %w", err)
	}
	// Open the store once so the per-project memory.db is created
	// immediately — otherwise /projects list shows "(no memory
	// yet)" for a freshly added project, which looks broken.
	store, err := memory.OpenProjectStore(dataDir, abs)
	if err != nil {
		return "", fmt.Errorf("projects: init store: %w", err)
	}
	_ = store.Close()

	if alreadyMapped {
		return fmt.Sprintf("Already registered: %s → %s\n(use /projects use %s to make it active)", abs, key, filepath.Base(abs)), nil
	}
	return fmt.Sprintf("Registered project:\n  path: %s\n  key:  %s\n\nMake it active with /projects use %s", abs, key, filepath.Base(abs)), nil
}

func projectsRemove(dataDir, target string) (string, error) {
	if target == "" {
		return "", fmt.Errorf("usage: /projects remove <path|key|basename>")
	}
	m := memory.LoadProjectsMap(dataDir)
	path, key, ok := resolveProject(m, target)
	if !ok {
		return "", fmt.Errorf("projects: no registered project matches %q", target)
	}
	delete(m, path)
	if err := memory.SaveProjectsMap(dataDir, m); err != nil {
		return "", fmt.Errorf("projects: save map: %w", err)
	}
	// Drop from the named-workspace list too (clears Active if it pointed here).
	ws := loadWorkspace(dataDir)
	if _, ok := ws.Remove(path); ok {
		_ = memory.SaveWorkspace(dataDir, ws)
	}
	// Per-project memory.db is intentionally kept on disk so that
	// re-registering the same project later restores the prior
	// memory. We only tell the user where it lives so they can
	// purge it manually if they really want to.
	dir := filepath.Join(dataDir, "projects", key)
	return fmt.Sprintf("Unregistered project:\n  path: %s\n  key:  %s\n  (memory preserved at %s — delete that folder to wipe)",
		path, key, dir), nil
}

func projectsInfo(dataDir, target string) string {
	if target == "" {
		return "usage: /projects info <path|key|basename>"
	}
	m := memory.LoadProjectsMap(dataDir)
	path, key, ok := resolveProject(m, target)
	if !ok {
		return fmt.Sprintf("No project matches %q", target)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Project: %s\n", filepath.Base(path))
	fmt.Fprintf(&b, "  path: %s\n", path)
	fmt.Fprintf(&b, "  key:  %s\n", key)

	dir := filepath.Join(dataDir, "projects", key)
	dbPath := filepath.Join(dir, "memory.db")
	fi, err := os.Stat(dbPath)
	if err != nil {
		b.WriteString("  memory: (not yet initialized)\n")
		return strings.TrimRight(b.String(), "\n")
	}
	fmt.Fprintf(&b, "  memory: %s (%.1f KB)\n", dbPath, float64(fi.Size())/1024)

	// Open the store read-only-ish to show recent entries. The
	// store is opened in normal mode (it would lazily migrate on
	// first call), but we never write through this path so it is
	// effectively read-only.
	store, err := memory.OpenStore(dir)
	if err != nil {
		fmt.Fprintf(&b, "  (could not open store: %v)\n", err)
		return strings.TrimRight(b.String(), "\n")
	}
	defer store.Close()
	entries, err := store.Recent("", 5)
	if err != nil {
		fmt.Fprintf(&b, "  (could not read entries: %v)\n", err)
		return strings.TrimRight(b.String(), "\n")
	}
	if len(entries) == 0 {
		b.WriteString("  (no memory entries yet)\n")
		return strings.TrimRight(b.String(), "\n")
	}
	fmt.Fprintf(&b, "  recent entries (%d shown):\n", len(entries))
	for _, e := range entries {
		content := strings.ReplaceAll(strings.TrimSpace(e.Content), "\n", " ")
		if len(content) > 80 {
			content = content[:80] + "…"
		}
		fmt.Fprintf(&b, "    [%s] %s %s: %s\n",
			e.ID, e.UpdatedAt.Format("2006-01-02"), e.Scope, content)
	}
	return strings.TrimRight(b.String(), "\n")
}

func projectsHelp() string {
	return `Projects — named workspaces (working dir = agent sandbox root):

  /projects                 — list projects (★ active, → current directory)
  /projects add [path]      — register a project (default: current directory)
  /projects use <X>         — make a project active (applies on next launch)
  /projects remove <X>      — unregister (memory preserved on disk)
  /projects info <X>        — show project memory details
  /projects help            — this message

Selecting a project sets the sandbox root and preferred model on the next
launch; it never changes the working directory of the running session (that
would break session/prompt-cache stability). <X> can be the full path, the
project name, the project key (e.g. SuperCli-10bc5793), or the directory
basename (case-insensitive).`
}
