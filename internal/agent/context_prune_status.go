package agent

import (
	"encoding/json"
	"strconv"
	"strings"
)

// commandExitForPrune reads only ctx_execute's generated framing or a complete
// JSON result. Never search stdout/stderr or a cut JSON preview for exit codes.
// storedHandle is the already-validated outer output reference, if any.
func commandExitForPrune(content, storedHandle string) (int, bool) {
	head := content
	if len(head) > 192 {
		head = head[:192]
	}
	head, _, _ = strings.Cut(head, "\n")
	if rest, ok := strings.CutPrefix(head, "error: command_failed "); ok {
		rest, timeout := strings.CutPrefix(rest, "timeout ")
		rest, ok = strings.CutPrefix(rest, "exit=")
		if !ok {
			return 0, false
		}
		end := strings.IndexAny(rest, " :")
		if end < 1 {
			return 0, false
		}
		exit, err := strconv.Atoi(rest[:end])
		if err != nil || exit == 0 || strconv.Itoa(exit) != rest[:end] || (timeout && exit != 124) {
			return 0, false
		}
		suffix := rest[end:]
		return exit, strings.HasPrefix(suffix, " (") || (!timeout && strings.HasPrefix(suffix, ": "))
	}
	// Retaining additional output appends a generated footer to otherwise intact
	// JSON. Remove only that validated footer; malformed/truncated JSON stays unknown.
	content = pruneStructuredBody(content, storedHandle)
	var result struct {
		ExitCode *int `json:"exit_code"`
	}
	if json.Unmarshal([]byte(content), &result) == nil && result.ExitCode != nil {
		return *result.ExitCode, true
	}
	return 0, false
}
