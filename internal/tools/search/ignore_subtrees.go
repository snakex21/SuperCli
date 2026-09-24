package search

import (
	"path/filepath"
	"strings"
)

// IsSkippedSubtree applies the shared directory policy within a requested root.
// Unlike a blanket .claude exclusion, it leaves settings and skills visible.
// Selecting the worktrees directory itself (or a checkout below it) is explicit
// intent to inspect those files, so the selected root is never excluded.
func IsSkippedSubtree(root, dir string) bool {
	if filepath.Clean(root) == filepath.Clean(dir) {
		return false
	}
	name := filepath.Base(dir)
	return IsSkippedDirectory(name) || isAgentWorktreeDir(filepath.Base(filepath.Dir(dir)), name)
}

func isAgentWorktreeDir(parent, name string) bool {
	return strings.EqualFold(name, "worktrees") && strings.EqualFold(parent, ".claude")
}

// An unanchored rg glob would also reject the explicitly requested checkout's
// ancestors. In that case leave traversal to rg and filter only descendants in
// searchPathIsSkipped; otherwise prune the copies before rg reads their files.
func pathContainsAgentWorktree(path string) bool {
	previous := ""
	for _, part := range strings.Split(strings.ReplaceAll(path, "\\", "/"), "/") {
		if isAgentWorktreeDir(previous, part) {
			return true
		}
		previous = part
	}
	return false
}
