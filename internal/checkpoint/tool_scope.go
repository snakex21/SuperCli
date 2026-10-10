package checkpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"supercli/internal/tools/sandbox"
)

var errBeforePinNotCurrent = errors.New("checkpoint before snapshot requires a successful capture retry after an earlier failure; tool did not run")

func (m *Manager) pinDownloadToolContext(ctx context.Context, name string, args json.RawMessage) (context.Context, error) {
	if name != "web_download" {
		return ctx, nil
	}
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return ctx, fmt.Errorf("checkpoint download arguments: %w", err)
	}
	if strings.TrimSpace(a.Path) == "" {
		return ctx, fmt.Errorf("checkpoint: web_download requires a path")
	}
	return sandbox.WithPinnedDownloadDestination(ctx, m.home, a.Path)
}

// A turn's baseline grows as new tools introduce paths. Already covered paths,
// including paths that did not exist, keep their first pre-mutation state.
func (t *Turn) ensureBeforeForTool(ctx context.Context, name string, args json.RawMessage) error {
	if err := t.mu.LockContext(ctx); err != nil {
		return err
	}
	defer t.mu.Unlock()
	roots, known, err := t.manager.toolScopeContext(ctx, name, args)
	if err != nil {
		return err
	}
	if !known {
		return t.ensureWholeBeforeLocked(ctx)
	}
	if len(roots) == 0 {
		// An authorized external export creates no workspace history. In
		// particular nil roots must not fall back to capturing the workspace.
		return nil
	}
	newRoots := make([]string, 0, len(roots))
	for _, root := range roots {
		if !scopeContains(t.scopeRoots, root) {
			newRoots = append(newRoots, root)
		}
	}
	if t.touched && len(newRoots) == 0 {
		return t.ensureBeforePinLocked(ctx)
	}
	if t.touched && t.beforePinned && t.active != nil && t.before != "" && !t.wholeWorkspace {
		absent, err := t.manager.snapshotRootsAbsent(ctx, newRoots)
		if err != nil {
			return err
		}
		if absent {
			// The immutable before tree already represents these paths' absence.
			// Keep its live pin and extend only the scope used by after capture.
			// EnterBoundMutation prevents completion from releasing the pin here.
			t.scopeRoots = append(t.scopeRoots, newRoots...)
			return nil
		}
	}
	if err := t.ensureActivePinsLocked(); err != nil {
		return err
	}
	// A failed capture can publish a pin before reporting a later I/O error.
	// Reuse is admitted only after a fully successful before capture.
	t.beforePinned = false
	var commit string
	if t.wholeWorkspace {
		// The full baseline already covers ordinary files and their absence.
		// Explicit ignored files need a baseline when first named by a file tool.
		commit, err = t.manager.captureSnapshotFilteredPinned(ctx, newRoots, t.before, t.scopeRoots, nil, true, t.active, "before")
	} else {
		commit, err = t.manager.captureSnapshotFilteredPinned(ctx, newRoots, t.before, t.scopeRoots, nil, false, t.active, "before")
	}
	if err != nil {
		return err
	}
	t.before, t.touched = commit, true
	t.beforePinned = true
	t.scopeRoots = append(t.scopeRoots, newRoots...)
	return nil
}

// Only a fresh absence check qualifies. Existing empty files/directories,
// symlinks and permission/I/O errors all keep the ordinary capture path.
func (m *Manager) snapshotRootsAbsent(ctx context.Context, roots []string) (bool, error) {
	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if _, err := os.Lstat(filepath.Join(m.home, filepath.FromSlash(root))); !os.IsNotExist(err) {
			return false, nil
		}
	}
	return true, ctx.Err()
}

func (m *Manager) toolScope(name string, args json.RawMessage) ([]string, bool, error) {
	return m.toolScopeContext(context.Background(), name, args)
}

func (m *Manager) toolScopeContext(ctx context.Context, name string, args json.RawMessage) ([]string, bool, error) {
	var a struct {
		Path      string `json:"path"`
		Src       string `json:"src"`
		Dest      string `json:"dest"`
		Action    string `json:"action"`
		TargetDir string `json:"target_dir"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, false, fmt.Errorf("checkpoint tool arguments: %w", err)
	}
	var paths []string
	switch name {
	case "web_download":
		if strings.TrimSpace(a.Path) == "" {
			return nil, false, fmt.Errorf("checkpoint: %s requires a path", name)
		}
		full, err := sandbox.ResolveDownloadDestination(ctx, m.home, a.Path)
		if err != nil {
			return nil, false, fmt.Errorf("checkpoint download path: %w", err)
		}
		canonicalHome, err := sandbox.ResolveWithin(m.home, ".")
		if err != nil {
			return nil, false, fmt.Errorf("checkpoint download workspace: %w", err)
		}
		if !sandbox.IsUnder(canonicalHome, full) {
			// The actual download owns the no-overwrite check. An external new
			// deliverable is outside this manager's workspace undo/redo scope.
			return nil, true, nil
		}
		paths = []string{full}
	case "write_file", "patch_file", "create_file", "make_dir", "trash":
		if strings.TrimSpace(a.Path) == "" {
			return nil, false, fmt.Errorf("checkpoint: %s requires a path", name)
		}
		paths = []string{a.Path}
	case "edit_docx", "edit_xlsx":
		if strings.TrimSpace(a.Path) == "" {
			return nil, false, fmt.Errorf("checkpoint: %s requires a path", name)
		}
		// Office edits also replace their adjacent backup.
		paths = []string{a.Path, a.Path + ".bak"}
	case "read_zip":
		if a.Action != "extract" || strings.TrimSpace(a.TargetDir) == "" {
			// A default extraction destination belongs to the tool instance. Keep
			// the full-workspace fallback rather than guessing a timestamp/root.
			return nil, false, nil
		}
		paths = []string{a.TargetDir}
	case "move", "copy":
		if strings.TrimSpace(a.Src) == "" || strings.TrimSpace(a.Dest) == "" {
			return nil, false, fmt.Errorf("checkpoint: %s requires src and dest", name)
		}
		source, err := sandbox.ResolveSafe(m.home, a.Src)
		if err != nil {
			return nil, false, err
		}
		destination, err := sandbox.ResolveSafe(m.home, a.Dest)
		if err != nil {
			return nil, false, err
		}
		if info, err := os.Stat(destination); err == nil && info.IsDir() {
			destination = filepath.Join(destination, filepath.Base(source))
		}
		paths = []string{source, destination}
	default:
		return nil, false, nil
	}
	canonicalHome, err := sandbox.ResolveWithin(m.home, ".")
	if err != nil {
		return nil, false, fmt.Errorf("checkpoint workspace: %w", err)
	}
	roots := make([]string, 0, len(paths))
	for _, p := range paths {
		full, err := sandbox.ResolveSafe(m.home, p)
		if err != nil {
			return nil, false, fmt.Errorf("checkpoint path: %w", err)
		}
		rel, err := filepath.Rel(canonicalHome, full)
		if err != nil || !within(canonicalHome, full) {
			return nil, false, fmt.Errorf("unsafe checkpoint path %q", p)
		}
		rel = filepath.ToSlash(rel)
		if rel != "." && !m.snapshotExcluded(rel) && !validNarrowPath(rel) {
			return nil, false, fmt.Errorf("invalid checkpoint selected path %q", rel)
		}
		roots = append(roots, rel)
	}
	return roots, true, nil
}

func scopeContains(roots []string, p string) bool {
	for _, root := range roots {
		if root == "." || pathEqual(p, root) || pathPrefix(p, root+"/") {
			return true
		}
	}
	return false
}
