// Package memory is the agent's long-term memory store. F2.b
// adds SQLite persistence with FTS5 search and markdown file
// sync; the API matches the design in .supercli/docs/F2-design.md.
package memory

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Source describes who created an entry. Stored as a string in DB.
const (
	SourceUser  = "user"
	SourceAgent = "agent"
	SourceTool  = "tool"
)

// ScopeKind returns the scope prefix and the on-disk file name.
// Recognised scopes:
//   - "general"             -> general.md
//   - "project:<hash>"      -> project-<hash>.md
//   - "scratch:<YYYY-MM-DD>" -> scratch-<YYYY-MM-DD>.md
//   - "pattern:<id>"        -> patterns/<id>.md
//   - any other value       -> scope-<sanitised>.md
func ScopeFile(root, scope string) (string, string, error) {
	if root == "" {
		return "", "", fmt.Errorf("memory.ScopeFile: root is empty")
	}
	if scope == "" {
		return "", "", fmt.Errorf("memory.ScopeFile: scope is empty")
	}
	if scope == "general" {
		return filepath.Join(root, "general.md"), scope, nil
	}
	colon := strings.IndexByte(scope, ':')
	kind := scope
	arg := ""
	if colon >= 0 {
		kind = scope[:colon]
		arg = scope[colon+1:]
	}
	switch kind {
	case "project":
		if arg == "" {
			return "", "", fmt.Errorf("memory.ScopeFile: project scope needs hash")
		}
		return filepath.Join(root, "project-"+sanitise(arg)+".md"), scope, nil
	case "scratch":
		if arg == "" {
			return "", "", fmt.Errorf("memory.ScopeFile: scratch scope needs date")
		}
		return filepath.Join(root, "scratch-"+sanitise(arg)+".md"), scope, nil
	case "pattern":
		if arg == "" {
			return "", "", fmt.Errorf("memory.ScopeFile: pattern scope needs id")
		}
		return filepath.Join(root, "patterns", sanitise(arg)+".md"), scope, nil
	default:
		return filepath.Join(root, "scope-"+sanitise(scope)+".md"), scope, nil
	}
}

var safeChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func sanitise(s string) string {
	out := safeChars.ReplaceAllString(s, "_")
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

// ProjectHash returns the 8-char hash of cwd used for project scope.
func ProjectHash(cwd string) string {
	h := sha1.Sum([]byte(cwd))
	return hex.EncodeToString(h[:])[:8]
}

// Entry is a single remembered fact or note.
type Entry struct {
	ID      string
	Scope   string
	Content string
	Tags    []string
	Source  string
	// FilePath is set by the Store when reading; callers do not
	// need to populate it on write.
	FilePath string
	// LineStart/LineEnd are set by the markdown sync when an
	// entry is read back; they let callers locate the entry
	// in the on-disk file for editing.
	LineStart int
	LineEnd   int

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Validate checks that an entry is well-formed before storage.
func (e Entry) Validate() error {
	if e.ID == "" {
		return fmt.Errorf("memory.Entry.Validate: id is empty")
	}
	if e.Content == "" {
		return fmt.Errorf("memory.Entry.Validate(%s): content is empty", e.ID)
	}
	if e.Scope == "" {
		return fmt.Errorf("memory.Entry.Validate(%s): scope is empty", e.ID)
	}
	if e.Source == "" {
		// default to "user" - matches the common case
		// and the design example.
	}
	switch e.Source {
	case "", SourceUser, SourceAgent, SourceTool:
		// ok
	default:
		return fmt.Errorf("memory.Entry.Validate(%s): invalid source %q", e.ID, e.Source)
	}
	return nil
}

// TagsCSV returns the tags joined by "," for storage. Empty if
// no tags.
func (e Entry) TagsCSV() string {
	if len(e.Tags) == 0 {
		return ""
	}
	return strings.Join(e.Tags, ",")
}

// EntriesFromCSV is the inverse of TagsCSV; empty => nil.
func EntriesFromCSV(csv string) []string {
	if csv == "" {
		return nil
	}
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
