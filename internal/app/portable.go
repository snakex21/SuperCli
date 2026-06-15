// Portable-mode data migration: SuperCli stores ALL of its data in
// a supercli-data/ directory next to the executable. Older builds
// kept global state in ~/.supercli; on first portable start the old
// tree is copied over (the original is left in place with a
// MOVED.txt marker, never deleted automatically).
package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// legacyDataDir returns the old ~/.supercli directory, or "" when
// the user home cannot be resolved.
func legacyDataDir() string {
	uh, err := os.UserHomeDir()
	if err != nil || uh == "" {
		return ""
	}
	return filepath.Join(uh, ".supercli")
}

// migrateLegacyData copies ~/.supercli into dataDir when dataDir
// does not exist yet. It returns a one-time user-facing message
// ("" when nothing was migrated). Rules:
//
//   - dataDir already exists  → do nothing (new location wins; the
//     old directory is never used and never overwritten).
//   - ~/.supercli missing     → do nothing (fresh install).
//   - otherwise               → copy everything, fix any absolute
//     references to the old directory inside projects.json, and
//     drop a MOVED.txt marker in the old directory. The original
//     data is NOT deleted.
func migrateLegacyData(dataDir string) (string, error) {
	old := legacyDataDir()
	if old == "" {
		return "", nil
	}
	if fi, err := os.Stat(old); err != nil || !fi.IsDir() {
		return "", nil
	}
	if _, err := os.Stat(dataDir); err == nil {
		return "", nil // both exist: keep using supercli-data
	}
	if same, _ := samePath(old, dataDir); same {
		return "", nil
	}
	if err := copyTree(old, dataDir); err != nil {
		// Leave no half-migrated directory behind: a partial copy
		// would block the next attempt (dataDir would exist).
		_ = os.RemoveAll(dataDir)
		return "", fmt.Errorf("migrate %s -> %s: %w", old, dataDir, err)
	}
	fixLegacyReferences(dataDir, old)
	marker := fmt.Sprintf(
		"SuperCli data moved on %s.\n\nNew location:\n  %s\n\nThis directory was left untouched as a backup. You can delete it\nonce you have verified the new location works.\n",
		time.Now().Format("2006-01-02 15:04:05"), dataDir)
	_ = os.WriteFile(filepath.Join(old, "MOVED.txt"), []byte(marker), 0o644)
	return fmt.Sprintf("migrated data to %s (original kept at %s — see MOVED.txt)", dataDir, old), nil
}

// samePath reports whether two paths point at the same location.
func samePath(a, b string) (bool, error) {
	aa, err := filepath.Abs(a)
	if err != nil {
		return false, err
	}
	bb, err := filepath.Abs(b)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(filepath.Clean(aa), filepath.Clean(bb)), nil
}

// copyTree recursively copies src into dst. Symlinks are skipped
// (the legacy layout never contained any); SQLite -wal/-shm side
// files are copied as-is, which is safe because migration runs
// before any database is opened.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil // skip symlinks/devices
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// fixLegacyReferences rewrites absolute paths that point INTO the
// old ~/.supercli directory inside the migrated projects.json.
// Project paths that merely point at user projects (the normal
// case) are left alone — they reference the projects themselves,
// not CLI data. Best-effort: a malformed file is left untouched.
func fixLegacyReferences(dataDir, oldRoot string) {
	path := filepath.Join(dataDir, "projects.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return
	}
	changed := false
	fixed := make(map[string]string, len(m))
	for k, v := range m {
		nk := rewriteUnder(k, oldRoot, dataDir)
		nv := rewriteUnder(v, oldRoot, dataDir)
		if nk != k || nv != v {
			changed = true
		}
		fixed[nk] = nv
	}
	if !changed {
		return
	}
	if out, err := json.MarshalIndent(fixed, "", "  "); err == nil {
		_ = os.WriteFile(path, out, 0o644)
	}
}

// rewriteUnder maps a path located under oldRoot to the equivalent
// path under newRoot; anything else is returned unchanged.
func rewriteUnder(p, oldRoot, newRoot string) string {
	if p == "" || oldRoot == "" {
		return p
	}
	cp, co := filepath.Clean(p), filepath.Clean(oldRoot)
	if strings.EqualFold(cp, co) {
		return newRoot
	}
	prefix := co + string(filepath.Separator)
	if len(cp) > len(prefix) && strings.EqualFold(cp[:len(prefix)], prefix) {
		return filepath.Join(newRoot, cp[len(prefix):])
	}
	return p
}
