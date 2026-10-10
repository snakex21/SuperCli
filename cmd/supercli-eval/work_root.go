package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// prepareEvalWorkRoot resolves only the implicit default. Explicit workspace
// paths keep their existing interpretation and are created by the eval runner.
func prepareEvalWorkRoot(explicit string, resolve func(string) (string, bool, error)) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	root, _, err := resolve("")
	if err != nil {
		return "", fmt.Errorf("resolve portable eval directory: %w", err)
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("resolve portable eval directory: runtime data root %q is not absolute", root)
	}
	workRoot := filepath.Join(root, "eval", "workspaces")
	if err := os.MkdirAll(workRoot, 0o755); err != nil {
		return "", fmt.Errorf("create portable eval work root %q: %w", workRoot, err)
	}
	return workRoot, nil
}
