package sandbox

import (
	"fmt"
	"path/filepath"
	"strings"
)

// sensitiveRoots are absolute paths that the sandbox
// refuses to touch regardless of home. The entries that
// start with "/" are Unix-only; on Windows they are
// skipped automatically (filepath.IsAbs check).
var sensitiveRoots = []string{
	"/dev",
	"/proc",
	"/sys",
	"/etc",
	"/var",
	"/boot",
	"/root",
	// Windows: system roots
	`C:\Windows\System32`,
	`C:\Windows\SysWOW64`,
	`C:\Program Files`,
	`C:\Program Files (x86)`,
}

// isSensitive reports whether abs is one of the
// well-known OS-controlled locations. The check is
// case-insensitive on Windows (filepath volume-aware),
// case-sensitive elsewhere.
func isSensitive(abs string) bool {
	abs = filepath.Clean(abs)
	absLower := strings.ToLower(abs)
	for _, root := range sensitiveRoots {
		root = filepath.Clean(root)
		if abs == root {
			return true
		}
		// Check if abs starts with root + separator.
		// On Windows we lowercase both for case-
		// insensitivity; on Unix we keep the case.
		if filepath.Separator == '\\' {
			if strings.HasPrefix(absLower, strings.ToLower(root)+string(filepath.Separator)) {
				return true
			}
		} else {
			if strings.HasPrefix(abs, root+string(filepath.Separator)) {
				return true
			}
		}
	}
	return false
}

// IsUnder reports whether child is the same as parent
// or a descendant of parent. Both arguments are
// expected to be absolute, cleaned paths. The check is
// purely lexical; it does NOT resolve symlinks.
func IsUnder(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	if parent == child {
		return true
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return !strings.HasPrefix(rel, "..")
}

// Unsandboxed, when true, skips the home-boundary check in
// ResolveSafe and AllowDestructive — the path still needs to
// exist and symlinks are still resolved, but the "outside home"
// rejection is disabled. Sensitive system paths are still
// blocked. Set at startup from --unsandboxed.
var Unsandboxed bool

// ResolveSafe joins rel onto home, evaluates any ".."
// and symlinks in the result, and returns the canonical
// absolute path. Returns ErrEscape if the result lands
// outside home (unless Unsandboxed is on), or if it
// points at a sensitive system root.
//
// If home is empty, an error is returned. The function
// tolerates a non-existent path: it returns the
// canonical would-be path as long as the path's
// ancestors can be walked. (We use evalSymlinks on the
// longest existing prefix.)
func ResolveSafe(home, rel string) (string, error) {
	if home == "" {
		return "", fmt.Errorf("sandbox: ResolveSafe: empty home")
	}
	home = filepath.Clean(home)
	absHome, err := filepath.Abs(home)
	if err != nil {
		return "", fmt.Errorf("sandbox: abs home: %w", err)
	}
	// Combine.
	var abs string
	if rel == "" {
		abs = absHome
	} else if filepath.IsAbs(rel) {
		abs = filepath.Clean(rel)
	} else {
		abs = filepath.Join(absHome, rel)
	}
	// Symlink resolution. Walk up the path until we
	// find an existing directory; resolve symlinks at
	// that level; then re-append the rest.
	resolved, err := evalSymlinksPrefix(abs)
	if err != nil {
		return "", fmt.Errorf("sandbox: resolve symlinks: %w", err)
	}
	// Refuse if outside home (skipped when unsandboxed).
	if !Unsandboxed && !IsUnder(absHome, resolved) {
		return "", ErrEscape
	}
	// Refuse if it lands on a sensitive system path
	// (always enforced, even when unsandboxed).
	if isSensitive(resolved) {
		return "", ErrDenied
	}
	return resolved, nil
}

// evalSymlinksPrefix resolves symlinks for the longest
// existing prefix of path and appends the rest. This
// lets us safely resolve a path that doesn't exist yet
// (e.g. a file we are about to create).
func evalSymlinksPrefix(p string) (string, error) {
	// Fast path: full path exists.
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved, nil
	}
	// Walk up until we find a parent that exists,
	// collecting the un-evaluated tail in parts.
	prefix := p
	var parts []string
	for {
		parent := filepath.Dir(prefix)
		if parent == prefix {
			// Reached the root without finding an
			// existing ancestor. Return the path
			// unchanged.
			return p, nil
		}
		parts = append([]string{filepath.Base(prefix)}, parts...)
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			if len(parts) == 0 {
				return resolved, nil
			}
			return filepath.Join(append([]string{resolved}, parts...)...), nil
		}
		prefix = parent
	}
}
