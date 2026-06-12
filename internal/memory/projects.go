package memory

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ProjectKey returns the stable per-project directory name:
// "<basename>-<8-char-hash>". The hash is over the cleaned,
// lower-cased absolute path so the same project on the same
// machine always maps to the same directory, while two
// directories with the same basename do not collide.
func ProjectKey(path string) string {
	clean := strings.ToLower(filepath.ToSlash(filepath.Clean(path)))
	h := sha1.Sum([]byte(clean))
	hash := hex.EncodeToString(h[:])[:8]
	base := sanitise(filepath.Base(filepath.Clean(path)))
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "project"
	}
	if len(base) > 32 {
		base = base[:32]
	}
	return base + "-" + hash
}

// projectsFile is the path of the path→directory map.
func projectsFile(home string) string {
	return filepath.Join(home, "projects.json")
}

// LoadProjectsMap reads <home>/projects.json (project path →
// directory key). The file is user-editable, so the loader is
// deliberately tolerant: a missing file is an empty map, and a
// malformed file is repaired (best effort) instead of aborting
// the program — memory must never block startup.
func LoadProjectsMap(home string) map[string]string {
	raw, err := os.ReadFile(projectsFile(home))
	if err != nil {
		return map[string]string{}
	}
	m := map[string]string{}
	if err := json.Unmarshal(raw, &m); err == nil {
		return m
	}
	if repaired, ok := repairJSONObject(string(raw)); ok {
		if err := json.Unmarshal([]byte(repaired), &m); err == nil {
			return m
		}
	}
	return map[string]string{}
}

// SaveProjectsMap writes the map back. Best effort: an error is
// returned but callers may ignore it (memory is non-fatal).
func SaveProjectsMap(home string, m map[string]string) error {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(projectsFile(home), raw, 0o644)
}

// repairJSONObject attempts to fix the most common manual-edit
// damage in a JSON object file: trailing commas before } and
// truncated content (e.g. a crash mid-write). It returns the
// repaired text and whether a repair was produced.
func repairJSONObject(s string) (string, bool) {
	t := strings.TrimSpace(s)
	if t == "" {
		return "{}", true
	}
	// Strip a UTF-8 BOM.
	t = strings.TrimPrefix(t, "\uFEFF")
	// Remove trailing commas before closing braces: `,"a":1,}`.
	for {
		next := removeTrailingComma(t)
		if next == t {
			break
		}
		t = next
	}
	// Truncated file: cut back to the last complete `"key": "value"`
	// pair and close the object.
	if strings.HasPrefix(t, "{") && !strings.HasSuffix(t, "}") {
		if i := strings.LastIndexByte(t, '"'); i > 0 {
			// Find the end of the last complete value string by
			// scanning for the last `"` that is followed only by
			// whitespace/comma garbage.
			t = strings.TrimRight(t[:i+1], ", \t\r\n")
			t += "}"
		} else {
			return "{}", true
		}
	}
	return t, true
}

func removeTrailingComma(t string) string {
	for i := len(t) - 1; i > 0; i-- {
		c := t[i]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			continue
		}
		if c == '}' || c == ']' {
			// look backwards for a comma directly before it
			for j := i - 1; j >= 0; j-- {
				cj := t[j]
				if cj == ' ' || cj == '\t' || cj == '\r' || cj == '\n' {
					continue
				}
				if cj == ',' {
					return t[:j] + t[j+1:]
				}
				return t
			}
		}
		return t
	}
	return t
}

// OpenProjectStore opens (creating if needed) the per-project
// memory store under <home>/projects/<key>/memory.db and records
// the path→key mapping in <home>/projects.json.
func OpenProjectStore(home, projectPath string) (*Store, error) {
	if home == "" || projectPath == "" {
		return nil, fmt.Errorf("memory.OpenProjectStore: home and projectPath are required")
	}
	key := ProjectKey(projectPath)
	dir := filepath.Join(home, "projects", key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("memory.OpenProjectStore: %w", err)
	}
	m := LoadProjectsMap(home)
	if m[projectPath] != key {
		m[projectPath] = key
		_ = SaveProjectsMap(home, m) // best effort
	}
	return OpenStore(dir)
}
