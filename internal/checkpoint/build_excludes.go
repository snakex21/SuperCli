package checkpoint

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// Build caches and outputs are not part of a checkpoint (1.0.38). Before,
// a whole-workspace snapshot also read compiler caches: in the reported
// orchestration every Zig build left a "zig-global" cache (thousands of
// files) inside the project, snapshots of it hit the 30 s completion limit,
// and parallel agents waited behind them for the shared checkpoint lock.
// Undo restores source; caches and outputs are rebuilt by the tools.

// DefaultCheckpointExcludes are directory names skipped at any depth. A
// config entry "!name" (checkpoint_exclude) re-includes one of them.
var DefaultCheckpointExcludes = []string{
	// Zig
	".zig-cache", "zig-cache", "zig-out", "zig-global", "zig-local", "zig-global-cache", "zig-local-cache",
	// JavaScript / web
	"node_modules", ".next", ".nuxt", ".turbo", ".parcel-cache", ".svelte-kit", "dist",
	// Rust / JVM / native build outputs
	"target", ".gradle", "build", "CMakeFiles",
	// Python
	"__pycache__", ".venv", "venv", ".pytest_cache", ".mypy_cache", ".ruff_cache", ".tox",
}

// WholeSnapshotBudget bounds a whole-workspace "before" snapshot of a
// command. Past it the command runs with a narrower checkpoint (its workdir)
// or none, with a visible note, instead of holding the turn's checkpoint
// lock (and every other agent's file write) for half a minute.
var WholeSnapshotBudget = 8 * time.Second

// scanHook is a test seam called for every path a snapshot visits.
var scanHook atomic.Pointer[func(rel string)]

func setSnapshotScanHook(fn func(string)) {
	if fn == nil {
		scanHook.Store(nil)
		return
	}
	scanHook.Store(&fn)
}

type excludeRules struct {
	names   map[string]bool // directory names skipped at any depth
	paths   []string        // workspace-relative directory prefixes
	pattern []string        // extra gitignore lines from config
}

func normalizeExcludeName(s string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(s)
	}
	return s
}

// SetExcludeRules applies the checkpoint_exclude config: "name" or
// "dir/sub/" adds an exclusion, "!name" re-includes a built-in one.
func (m *Manager) SetExcludeRules(rules []string) {
	names := map[string]bool{}
	for _, n := range DefaultCheckpointExcludes {
		names[normalizeExcludeName(n)] = true
	}
	var paths, pattern []string
	for _, raw := range rules {
		r := strings.TrimSpace(filepath.ToSlash(raw))
		if r == "" || strings.HasPrefix(r, "#") {
			continue
		}
		if strings.HasPrefix(r, "!") {
			delete(names, normalizeExcludeName(strings.Trim(strings.TrimPrefix(r, "!"), "/")))
			continue
		}
		trimmed := strings.Trim(r, "/")
		if trimmed == "" || strings.Contains(trimmed, "..") {
			continue
		}
		if strings.ContainsAny(trimmed, "*?[") {
			pattern = append(pattern, r)
			continue
		}
		if strings.Contains(trimmed, "/") {
			paths = append(paths, path.Clean(trimmed))
		} else {
			names[normalizeExcludeName(trimmed)] = true
		}
	}
	m.excludeMu.Lock()
	// Command-detected cache paths survive config reloads.
	m.rules = excludeRules{names: names, paths: append(paths, m.dynamicPaths...), pattern: pattern}
	m.refreshExcludesLocked()
	m.excludeMu.Unlock()
}

// addDynamicExclude records a cache directory a command declared (for
// example ZIG_GLOBAL_CACHE_DIR=run/zig-global). rel is workspace-relative.
func (m *Manager) addDynamicExclude(rel string) bool {
	rel = path.Clean(filepath.ToSlash(rel))
	if rel == "." || rel == "" || strings.HasPrefix(rel, "../") || rel == ".." {
		return false
	}
	m.excludeMu.Lock()
	defer m.excludeMu.Unlock()
	m.ensureRulesLocked()
	for _, p := range m.dynamicPaths {
		if pathEqual(p, rel) {
			return false
		}
	}
	m.dynamicPaths = append(m.dynamicPaths, rel)
	m.rules.paths = append(m.rules.paths, rel)
	m.refreshExcludesLocked()
	return true
}

// ensureRulesLocked initializes the rules of a Manager not built by Open
// (tests construct legacy managers with a literal excludes string, which then
// is the base). Caller holds excludeMu.
func (m *Manager) ensureRulesLocked() {
	if m.rules.names != nil {
		return
	}
	if m.baseExcludes == "" {
		m.baseExcludes = m.excludes
	}
	m.initRulesLocked()
	m.refreshExcludesLocked()
}

func (m *Manager) initRulesLocked() {
	names := map[string]bool{}
	for _, n := range DefaultCheckpointExcludes {
		names[normalizeExcludeName(n)] = true
	}
	m.rules = excludeRules{names: names}
}

// refreshExcludesLocked rebuilds the private repository's info/exclude
// content; ensureRepoLocked writes it when it changed. Caller holds excludeMu.
func (m *Manager) refreshExcludesLocked() {
	var b strings.Builder
	b.WriteString(m.baseExcludes)
	names := make([]string, 0, len(m.rules.names))
	for n := range m.rules.names {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		b.WriteString(escapeExcludePath(n) + "/\n")
	}
	for _, p := range m.rules.paths {
		b.WriteString("/" + escapeExcludePath(p) + "/\n")
	}
	for _, p := range m.rules.pattern {
		b.WriteString(p + "\n")
	}
	if content := b.String(); content != m.excludes {
		m.excludes = content
		m.excludesDirty = true
	}
}

// currentExcludes returns the info/exclude content and whether it changed
// since the repository last wrote it.
func (m *Manager) currentExcludes() (string, bool) {
	m.excludeMu.Lock()
	defer m.excludeMu.Unlock()
	m.ensureRulesLocked()
	return m.excludes, m.excludesDirty
}

func (m *Manager) markExcludesWritten(content string) {
	m.excludeMu.Lock()
	if m.excludes == content {
		m.excludesDirty = false
	}
	m.excludeMu.Unlock()
}

// buildExcluded reports whether a workspace-relative path lies in a build
// cache/output directory (built-in names, config, command-declared caches).
func (m *Manager) buildExcluded(p string) bool {
	m.excludeMu.Lock()
	defer m.excludeMu.Unlock()
	m.ensureRulesLocked()
	parts := strings.Split(p, "/")
	// The last segment of a file path is a file name, not a directory.
	dirs := parts
	if len(dirs) > 0 {
		dirs = dirs[:len(dirs)-1]
	}
	for _, part := range dirs {
		if m.rules.names[normalizeExcludeName(part)] {
			return true
		}
	}
	for _, prefix := range m.rules.paths {
		if pathEqual(p, prefix) || hasPathPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}

// buildExcludedDir is buildExcluded for a directory path (its own name is a
// directory name).
func (m *Manager) buildExcludedDir(p string) bool {
	if p == "" || p == "." {
		return false
	}
	return m.buildExcluded(strings.TrimSuffix(p, "/") + "/x")
}

func hasPathPrefix(p, prefix string) bool {
	if runtime.GOOS == "windows" {
		return strings.HasPrefix(strings.ToLower(p), strings.ToLower(prefix))
	}
	return strings.HasPrefix(p, prefix)
}

var zigCacheArg = regexp.MustCompile(`(?:ZIG_(?:GLOBAL|LOCAL)_CACHE_DIR\s*[=:]\s*|--(?:global-)?cache-dir[\s=]+)(?:\\?["'])?([^"'\s;,|&)\]]+)`)

// noteCommandCaches detects cache directories a command declares for Zig
// (environment variables or --cache-dir / --global-cache-dir flags) and
// excludes those inside the workspace from snapshots.
func (m *Manager) noteCommandCaches(args []byte, workdir string) {
	for _, match := range zigCacheArg.FindAllSubmatch(args, -1) {
		dir := strings.TrimRight(string(match[1]), `\`)
		if dir == "" {
			continue
		}
		dir = strings.ReplaceAll(dir, `\\`, `\`)
		var full string
		if filepath.IsAbs(dir) {
			full = filepath.Clean(dir)
		} else {
			base := m.home
			if workdir != "" {
				base = filepath.Join(m.home, filepath.FromSlash(workdir))
			}
			full = filepath.Join(base, filepath.FromSlash(dir))
		}
		if !within(m.home, full) || pathEqual(full, m.home) {
			continue
		}
		rel, err := filepath.Rel(m.home, full)
		if err != nil {
			continue
		}
		m.addDynamicExclude(rel)
	}
}

// budgetedSnapshot runs a whole-workspace capture under WholeSnapshotBudget.
// Running out of budget is a snapshot limit (the caller narrows or skips the
// checkpoint with a note), not a failure of the command.
func budgetedSnapshot(ctx context.Context, capture func(context.Context) error) error {
	budget := WholeSnapshotBudget
	if budget <= 0 {
		return capture(ctx)
	}
	bounded, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	err := capture(bounded)
	if err != nil && ctx.Err() == nil && (errors.Is(err, context.DeadlineExceeded) || bounded.Err() != nil) {
		return snapshotLimit(fmt.Sprintf("workspace snapshot took longer than %s (large directory skipped / pominięto duży katalog)", budget))
	}
	return err
}
