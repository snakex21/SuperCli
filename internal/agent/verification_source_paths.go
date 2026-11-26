package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"supercli/internal/system/childproc"
)

// Only an edit of project source makes earlier checks stale. Files a build
// or test writes for itself (outputs, caches, logs, packages) and files the
// project's .gitignore excludes are not what the checks verified; editing
// them (for example a note in zig-out/ or a log in a cache folder) must not
// block closing a goal. Commands never invalidate checks at all: only file
// tools do, so a build creating new output files is not a change.

// outputDirNames are folder names that hold generated files in common
// toolchains. Outside a git work tree, a path under one of them is not
// source; inside one, .gitignore decides instead.
var outputDirNames = map[string]bool{
	"zig-out": true, ".zig-cache": true, "zig-cache": true,
	"node_modules": true, "dist": true, "target": true, "out": true, "bin": true, "obj": true,
	"__pycache__": true, ".pytest_cache": true, ".mypy_cache": true, ".tox": true, ".venv": true,
	".gradle": true, ".next": true, ".nuxt": true, "coverage": true, ".cache": true,
	".git": true, ".supercli": true,
}

// sourceMutationPaths returns the edited paths that are project source,
// relative to baseDir where possible. ok is false when the edit names no
// path (unknown: treat as a source change).
func sourceMutationPaths(baseDir string, paths []string) (source []string, ok bool) {
	if len(paths) == 0 {
		return nil, false
	}
	rels := make([]string, 0, len(paths))
	for _, p := range paths {
		rels = append(rels, relToBase(baseDir, p))
	}
	// In a git work tree git decides: ignored and untracked is not source,
	// tracked is source even in a folder named "bin". Elsewhere the common
	// output folder names decide.
	ignored, isRepo := gitIgnored(baseDir, rels)
	for _, rel := range rels {
		if isRepo && ignored[rel] || !isRepo && isOutputPath(rel) {
			continue
		}
		source = append(source, rel)
	}
	return source, true
}

func relToBase(baseDir, p string) string {
	p = filepath.Clean(filepath.FromSlash(strings.TrimSpace(p)))
	if baseDir != "" && filepath.IsAbs(p) {
		if rel, err := filepath.Rel(baseDir, p); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			p = rel
		}
	}
	return filepath.ToSlash(p)
}

func isOutputPath(rel string) bool {
	parts := strings.Split(strings.ToLower(rel), "/")
	for _, part := range parts[:max(0, len(parts)-1)] {
		if outputDirNames[part] {
			return true
		}
	}
	return false
}

// gitIgnored asks git which of the paths the project ignores (tracked files
// are never reported). isRepo is false outside a git work tree; when git is
// missing or slow, nothing is treated as ignored.
func gitIgnored(baseDir string, rels []string) (ignored map[string]bool, isRepo bool) {
	out := map[string]bool{}
	if baseDir == "" || len(rels) == 0 {
		return out, false
	}
	if _, err := os.Stat(filepath.Join(baseDir, ".git")); err != nil {
		return out, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", baseDir, "check-ignore", "--stdin")
	cmd.Stdin = strings.NewReader(strings.Join(rels, "\n") + "\n")
	childproc.HideWindow(cmd)
	// Exit status 1 means "none ignored"; any other failure means unknown.
	stdout, _ := childproc.Output(cmd) // the timeout ends the whole git tree
	for _, line := range strings.Split(strings.ReplaceAll(string(stdout), "\r\n", "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out[filepath.ToSlash(line)] = true
		}
	}
	return out, true
}
