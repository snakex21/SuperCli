package search

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// openFile is os.Open under a tools-package alias.
func openFile(path string) (io.ReadCloser, error) { return os.Open(path) }

// WalkFiles walks root recursively, calling fn for each regular
// file. It skips common build / dependency directories to keep
// the search fast. The walk stops when fn returns errStopWalk.
// Exported because it is THE shared ignore-aware tree walk:
// search_code's rg fallback and the manifest noop-gate both use
// it, so the ignore set cannot drift between them.
func WalkFiles(root string, fn func(path string) error) error {
	skip := map[string]bool{
		".git": true, "node_modules": true, "vendor": true,
		"target": true, "dist": true, "build": true,
		".next": true, ".cache": true, "__pycache__": true,
		".venv": true, "venv": true, ".supercli": true,
	}
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Permission errors on individual files are
			// not fatal: keep walking.
			if os.IsPermission(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			if skip[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		return fn(path)
	})
}

// searchRootDirs returns the candidate roots we look at when
// rg is not available. It prefers the user's WorkDir and walks
// downward.
func searchRootDirs(workDir string) []string {
	if workDir == "" {
		workDir = "."
	}
	abs, err := filepath.Abs(workDir)
	if err != nil {
		abs = workDir
	}
	return []string{abs}
}

// matchLine returns true if line (case-insensitively) contains
// query. It is the core primitive shared by both rg-fallback
// and the tool tests.
func matchLine(line, query string) bool {
	return strings.Contains(strings.ToLower(line), strings.ToLower(query))
}

// noopWriter is here to keep io import live if all readers
// are inlined later.
var _ = io.Discard

// contextError is a tiny helper that returns ctx.Err() or nil.
func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
