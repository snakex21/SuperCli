package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// openProbeLog prepares parents only for the implicit portable log. Explicit
// paths keep their existing relative-path and missing-parent behavior.
func openProbeLog(explicit string, resolve func(string) (string, bool, error)) (*os.File, string, error) {
	logPath := explicit
	if logPath == "" {
		root, _, err := resolve("")
		if err != nil {
			return nil, "", fmt.Errorf("resolve portable log directory: %w", err)
		}
		if !filepath.IsAbs(root) {
			return nil, "", fmt.Errorf("resolve portable log directory: runtime data root %q is not absolute", root)
		}
		logPath = filepath.Join(root, "logs", "wire.jsonl")
		if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
			return nil, "", fmt.Errorf("create portable log directory %q: %w", filepath.Dir(logPath), err)
		}
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, "", fmt.Errorf("open log %q: %w", logPath, err)
	}
	return f, logPath, nil
}
