package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// ProjectInstructionFiles lists the repository-level instruction files other
// coding agents already use, in precedence order. Only the first existing
// file is used so a repo carrying both AGENTS.md and CLAUDE.md is not charged
// twice for (usually) duplicated content.
var ProjectInstructionFiles = []string{"AGENTS.md", "CLAUDE.md"}

// maxProjectInstructionBytes bounds the per-request cost of a project file.
// The block is part of the stable system prefix, so it is cached by providers
// that support prompt caching, but local models still pay for it once.
const maxProjectInstructionBytes = 32 * 1024

// LoadProjectInstructions reads AGENTS.md (or CLAUDE.md) from the workspace,
// walking up to the enclosing git root when the workspace itself has none.
// It is read once per session; the returned block is byte-stable so it does
// not invalidate the provider prompt cache.
func LoadProjectInstructions(home string) string {
	path := FindProjectInstructions(home)
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	truncated := false
	if len(b) > maxProjectInstructionBytes {
		b = b[:maxProjectInstructionBytes]
		for len(b) > 0 && !utf8.Valid(b) {
			b = b[:len(b)-1]
		}
		truncated = true
	}
	text := strings.TrimSpace(string(b))
	if text == "" {
		return ""
	}
	out := "Project instructions (" + filepath.Base(path) + "). Follow them for work in this repository; the current user request, safety rules, and tool rules take priority:\n" + text
	if truncated {
		out += "\n[truncated; read " + path + " for the rest]"
	}
	return out
}

// FindProjectInstructions returns the instruction file path that
// LoadProjectInstructions would use, or "" when none exists.
func FindProjectInstructions(home string) string {
	home = strings.TrimSpace(home)
	if home == "" {
		return ""
	}
	dir, err := filepath.Abs(home)
	if err != nil {
		return ""
	}
	// Without an enclosing git repo only the workspace itself is checked, so a
	// stray AGENTS.md in a home or Desktop folder never leaks into unrelated
	// workspaces.
	return findProjectInstructionFile(dir, gitRoot(dir))
}

// Keep traversal separate from root discovery so the no-repository policy can
// also be tested when portable TEMP is itself located inside a Git checkout.
func findProjectInstructionFile(dir, root string) string {
	for {
		for _, name := range ProjectInstructionFiles {
			p := filepath.Join(dir, name)
			if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
				return p
			}
		}
		if root == "" || dir == root {
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func gitRoot(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
