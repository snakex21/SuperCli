// Package manifest fingerprints a working tree cheaply so a batch
// operation can detect "nothing changed since the last identical
// run" WITHOUT a single LLM call (the noop-gate, config `noop_gate`).
//
// The fingerprint folds each file's relative path, size and mtime
// into one aggregate sha256 — file CONTENT is never read. mtime+size
// is a heuristic signal with the right failure mode for a gate:
// a false positive ("something changed" when nothing did, e.g. a
// touch) merely costs one normal run, while a false negative (a real
// change missed) is nearly impossible because any edit moves mtime
// or size. The gate must always FAIL OPEN: callers treat any error
// as "run normally".
package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"supercli/internal/tools/search"
)

// Record is the persisted manifest for one (project root, operation)
// pair: the tree hash after the last successful run, when it ran,
// and a human-readable operation excerpt for debugging.
type Record struct {
	Hash      string    `json:"hash"`
	UpdatedAt time.Time `json:"updatedAt"`
	Operation string    `json:"operation"`
}

// Compute walks root — reusing the shared ignore-aware walk from
// tools/search (.git, node_modules, vendor, .supercli, ...) — and
// returns the aggregate tree hash. Paths under any of extraIgnore
// (absolute directory paths, e.g. a portable data dir living inside
// the project) are excluded so state written by SuperCli itself
// never invalidates the manifest. Per-file stat errors are skipped:
// a transient unreadable file must not break the gate.
func Compute(root string, extraIgnore ...string) (string, error) {
	skip := make([]string, 0, len(extraIgnore))
	for _, dir := range extraIgnore {
		if dir == "" {
			continue
		}
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		skip = append(skip, strings.ToLower(filepath.Clean(dir))+string(filepath.Separator))
	}
	h := sha256.New()
	err := search.WalkFiles(root, func(path string) error {
		abs := path
		if a, err := filepath.Abs(path); err == nil {
			abs = a
		}
		low := strings.ToLower(abs) + string(filepath.Separator)
		for _, s := range skip {
			if strings.HasPrefix(low, s) {
				return nil
			}
		}
		fi, err := os.Stat(path)
		if err != nil || !fi.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		fmt.Fprintf(h, "%s\x00%d\x00%d\n", filepath.ToSlash(rel), fi.Size(), fi.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// OpKey returns a short stable key for an operation (the batch
// prompt), used as the manifest filename so different prompts keep
// independent manifests.
func OpKey(op string) string {
	s := sha256.Sum256([]byte(op))
	return hex.EncodeToString(s[:])[:16]
}

// Load reads a Record from path. A missing or unreadable file is an
// error — callers fail open (run normally).
func Load(path string) (Record, error) {
	var r Record
	b, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, fmt.Errorf("manifest: parse %s: %w", path, err)
	}
	return r, nil
}

// Save writes a Record to path, creating parent directories.
func Save(path string, r Record) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
