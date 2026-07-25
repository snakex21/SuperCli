// Package fileops implements F24: targeted file operations.
// Instead of loading an entire file to change one line, the
// model uses these functions to read/edit specific line
// ranges — saving tokens on large files.
//
// All line numbers are 1-based (matching editor convention).
// The package is pure — no sandbox, no LLM, just file I/O.
// Each mutation function returns a human-readable diff
// (±3 context lines) for verification.
package fileops

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

type WriteResult struct {
	// Created is true when the file did not exist before and was
	// newly created; false when an existing file was overwritten.
	Created bool
	// Bytes is the number of content bytes written.
	Bytes int
}

// WriteFile writes content to path, creating any missing parent
// directories. It reports whether the file was newly created or an
// existing one overwritten, plus the byte count — enough for the
// caller to state the change without re-reading the file.
//
// The package stays pure: WriteFile does NOT enforce the sandbox.
// Callers (the write_file tool) resolve the path with
// sandbox.ResolveSafe BEFORE calling this, exactly as ctx_execute
// does — keeping the safety boundary in one place (the tool).
func WriteFile(path, content string) (WriteResult, error) {
	if path == "" {
		return WriteResult{}, fmt.Errorf("fileops.WriteFile: empty path")
	}
	release := LockMutationPaths(path)
	defer release()
	// Overwriting a binary file with text destroys it irrecoverably (no
	// backup is taken here), and writing text to a path NAMED like a
	// binary document produces a file its application cannot open.
	//
	// A file that is merely in the WRONG TEXT ENCODING is the opposite
	// case: overwriting it with UTF-8 is the repair. This is the only
	// path that can perform that repair — the line tools and create_file
	// all refuse such a file — so it must not be blocked here.
	if err := EnsureOverwritableFile(path); err != nil {
		return WriteResult{}, err
	}
	_, statErr := os.Stat(path)
	existed := statErr == nil

	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return WriteResult{}, FileErr(err, dir)
		}
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return WriteResult{}, FileErr(err, path)
	}
	return WriteResult{Created: !existed, Bytes: len(content)}, nil
}

// MakeDir creates the directory at path, including any missing
// parents (like `mkdir -p`). It returns created=false when the
// directory already existed (idempotent, no error), and an error
// only when the path exists as a FILE or the mkdir fails.
//
// Like WriteFile, the package stays pure: the sandbox boundary is
// enforced by the calling tool via sandbox.ResolveSafe.
func MakeDir(path string) (created bool, err error) {
	if path == "" {
		return false, fmt.Errorf("fileops.MakeDir: empty path")
	}
	release := LockMutationPaths(path)
	defer release()
	if info, statErr := os.Stat(path); statErr == nil {
		if !info.IsDir() {
			return false, fmt.Errorf("fileops.MakeDir: %q exists and is a file, not a folder", path)
		}
		return false, nil // already a dir: idempotent success
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return false, fmt.Errorf("fileops.MakeDir: %w", err)
	}
	return true, nil
}

// Move renames/moves src to dst (works for files and folders, like
// `mv` / `git mv`). It is non-destructive by contract:
//
//   - if dst is an existing FOLDER, src is moved INTO it keeping its
//     base name (the familiar `mv file dir/` behaviour);
//   - it NEVER overwrites: if the final destination already exists,
//     it returns an error instead of clobbering — the caller should
//     ask the user how to proceed.
//
// Returns the final destination path actually used (after the
// move-into-folder adjustment) so the caller can report it.
// Pure: the sandbox is enforced by the tool on BOTH src and dst.
func Move(src, dst string) (finalDst string, err error) {
	if src == "" || dst == "" {
		return "", fmt.Errorf("fileops.Move: src and dst are required")
	}
	release := LockMutationPaths(src, dst)
	defer release()
	if _, err := os.Lstat(src); err != nil {
		return "", FileErr(err, src)
	}
	// move-INTO-folder convenience.
	if info, err := os.Lstat(dst); err == nil {
		if info.IsDir() && dst != src {
			dst = filepath.Join(dst, filepath.Base(src))
		}
	}
	// no-overwrite rule (re-check after the adjustment).
	if _, err := os.Lstat(dst); err == nil {
		return "", fmt.Errorf("fileops.Move: destination %q already exists; refusing to overwrite", dst)
	}
	if parent := filepath.Dir(dst); parent != "" {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return "", fmt.Errorf("fileops.Move: mkdir parent: %w", err)
		}
	}
	if err := os.Rename(src, dst); err != nil {
		return "", fmt.Errorf("fileops.Move: %w", err)
	}
	return dst, nil
}

// Copy copies src to dst (file or folder; folders are copied
// recursively, like `cp -r`). Same non-destructive contract as
// Move: if dst is an existing folder, src is copied INTO it; it
// NEVER overwrites an existing destination. Symlinks are skipped
// so a link target cannot smuggle data outside the tree.
//
// Returns the final destination path used. Pure: the tool enforces
// the sandbox on both src and dst.
func Copy(src, dst string) (finalDst string, err error) {
	if src == "" || dst == "" {
		return "", fmt.Errorf("fileops.Copy: src and dst are required")
	}
	release := LockMutationPaths(src, dst)
	defer release()
	srcInfo, err := os.Lstat(src)
	if err != nil {
		return "", FileErr(err, src)
	}
	if info, err := os.Lstat(dst); err == nil {
		if info.IsDir() && dst != src {
			dst = filepath.Join(dst, filepath.Base(src))
		}
	}
	if _, err := os.Lstat(dst); err == nil {
		return "", fmt.Errorf("fileops.Copy: destination %q already exists; refusing to overwrite", dst)
	}
	if srcInfo.IsDir() {
		if err := copyTree(src, dst); err != nil {
			return "", fmt.Errorf("fileops.Copy: %w", err)
		}
	} else {
		if err := copyFileContents(src, dst); err != nil {
			return "", fmt.Errorf("fileops.Copy: %w", err)
		}
	}
	return dst, nil
}

// copyTree copies a directory tree from src to dst. dst must not
// exist. Symlinks are skipped.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFileContents(p, target)
	})
}

// copyFileContents copies a single file's bytes from src to dst,
// creating dst's parent directory if needed.
func copyFileContents(src, dst string) error {
	if parent := filepath.Dir(dst); parent != "" {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return err
		}
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Trash moves src into trashDir under a timestamped name, instead
// of deleting it — so removal is always recoverable (the user can
// move it back out). Name collisions within the same second are
// disambiguated with a counter suffix. now is injected so the
// timestamp is testable (no hidden clock dependency).
//
// Returns the path the item was moved to. Pure: the tool resolves
// src against the sandbox and supplies trashDir under home.
func Trash(src, trashDir string, now time.Time) (dst string, err error) {
	if src == "" {
		return "", fmt.Errorf("fileops.Trash: empty path")
	}
	release := LockMutationPaths(src, trashDir)
	defer release()
	if _, err := os.Lstat(src); err != nil {
		return "", FileErr(err, src)
	}
	if err := os.MkdirAll(trashDir, 0o755); err != nil {
		return "", fmt.Errorf("fileops.Trash: prepare trash folder: %w", err)
	}
	stamp := now.Format("20060102-150405")
	base := filepath.Base(src)
	dst = filepath.Join(trashDir, stamp+"_"+base)
	for i := 1; ; i++ {
		if _, err := os.Lstat(dst); err != nil {
			break
		}
		dst = filepath.Join(trashDir, fmt.Sprintf("%s-%d_%s", stamp, i, base))
	}
	if err := os.Rename(src, dst); err != nil {
		return "", fmt.Errorf("fileops.Trash: %w", err)
	}
	return dst, nil
}
