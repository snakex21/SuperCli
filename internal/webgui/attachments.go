package webgui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"supercli/internal/tools/sandbox"
)

const maxChatAttachments = 8

// buildAttachmentAddon validates user-selected files through the same sandbox
// used by file and Office tools. The returned block is sent once with the next
// user message but is not persisted as visible transcript content.
func buildAttachmentAddon(home string, paths []string) (string, error) {
	if len(paths) == 0 {
		return "", nil
	}
	if len(paths) > maxChatAttachments {
		return "", fmt.Errorf("too many files: %d (maximum %d)", len(paths), maxChatAttachments)
	}
	absHome, err := filepath.Abs(home)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	seen := make(map[string]struct{}, len(paths))
	selected := make([]string, 0, len(paths))
	for _, raw := range paths {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		resolved, err := sandbox.ResolveSafe(absHome, raw)
		if err != nil {
			return "", fmt.Errorf("%q is outside the active workspace or blocked", raw)
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return "", fmt.Errorf("%q: %w", raw, err)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("%q is not a regular file", raw)
		}
		key := resolved
		if filepath.Separator == '\\' {
			key = strings.ToLower(key)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		display := resolved
		if sandbox.IsUnder(absHome, resolved) {
			if rel, relErr := filepath.Rel(absHome, resolved); relErr == nil {
				display = rel
			}
		}
		selected = append(selected, display)
	}
	if len(selected) == 0 {
		return "", nil
	}
	var out strings.Builder
	out.WriteString("[attached_files]\nThe user selected these files for this request. Inspect them with the appropriate file or Office tools before answering or editing:\n")
	for _, path := range selected {
		out.WriteString("- ")
		out.WriteString(strconv.Quote(path))
		out.WriteByte('\n')
	}
	out.WriteString("[/attached_files]")
	return out.String(), nil
}
