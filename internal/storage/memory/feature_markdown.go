package memory

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// mdHeader is the regex that matches an entry header line in
// the markdown file:
//
//	## <id> (<rfc3339>, <source>)
//
// The header and the content lines that follow (until the next
// "## " or EOF) form one entry.
var mdHeader = regexp.MustCompile(`^## ([A-Za-z0-9._-]+) \(([^)]+), ([^)]+)\)\s*$`)

// mdRead parses a memory markdown file and returns the entries
// it contains, in file order. The returned entries have
// FilePath, LineStart and LineEnd populated.
//
// A file that does not exist returns an empty slice, not an
// error - Put may create the file.
func mdRead(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("memory.mdRead: open %s: %w", path, err)
	}
	defer f.Close()

	var entries []Entry
	scope := scopeFromPath(path)
	var cur *Entry
	var lineNo int
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if m := mdHeader.FindStringSubmatch(line); m != nil {
			if cur != nil {
				cur.LineEnd = lineNo - 1
				entries = append(entries, *cur)
			}
			ts, _ := time.Parse(time.RFC3339, m[2])
			cur = &Entry{
				ID:        m[1],
				Scope:     scope,
				Source:    m[3],
				CreatedAt: ts,
				UpdatedAt: ts,
				FilePath:  path,
				LineStart: lineNo,
			}
			continue
		}
		if cur != nil {
			if cur.Content == "" {
				cur.Content = line
			} else {
				cur.Content += "\n" + line
			}
		}
	}
	if cur != nil {
		cur.LineEnd = lineNo
		// trim trailing whitespace that came from the
		// separator blank line in the file
		cur.Content = strings.TrimRight(cur.Content, " \t\r\n")
		entries = append(entries, *cur)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("memory.mdRead: scan %s: %w", path, err)
	}
	return entries, nil
}

// mdUpsert writes entry into path. If an entry with the same ID
// already exists, its block is replaced in place; otherwise the
// new block is appended at the end. The file is created if it
// does not exist.
//
// If e.Scope is empty, it is derived from the file name (e.g.
// "general.md" -> "general", "project-abc.md" -> "project:abc").
func mdUpsert(path string, e Entry) error {
	if e.Scope == "" {
		if s := scopeFromPath(path); s != "" {
			e.Scope = s
		}
	}
	if err := e.Validate(); err != nil {
		return err
	}
	entries, err := mdRead(path)
	if err != nil {
		return err
	}
	found := false
	for i := range entries {
		if entries[i].ID == e.ID {
			entries[i] = e
			entries[i].FilePath = path
			found = true
			break
		}
	}
	if !found {
		e.FilePath = path
		entries = append(entries, e)
	}
	return mdWrite(path, entries)
}

// mdDelete removes the entry with the given ID from path. It is
// a no-op if the ID is not present.
func mdDelete(path, id string) error {
	entries, err := mdRead(path)
	if err != nil {
		return err
	}
	out := entries[:0]
	removed := false
	for _, e := range entries {
		if e.ID == id {
			removed = true
			continue
		}
		out = append(out, e)
	}
	if !removed {
		return nil
	}
	return mdWrite(path, out)
}

// mdWrite writes entries to path in canonical form. Existing
// files are replaced atomically (write to a sibling, rename).
func mdWrite(path string, entries []Entry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("memory.mdWrite: mkdir %s: %w", filepath.Dir(path), err)
	}
	var b strings.Builder
	b.WriteString("# ")
	scope := scopeFromPath(path)
	b.WriteString(scopeTitle(scope))
	b.WriteString(" memory\n\n")
	for _, e := range entries {
		b.WriteString("## ")
		b.WriteString(e.ID)
		b.WriteString(" (")
		ts := e.UpdatedAt
		if ts.IsZero() {
			ts = e.CreatedAt
		}
		if ts.IsZero() {
			ts = time.Now().UTC()
		}
		b.WriteString(ts.UTC().Format(time.RFC3339))
		b.WriteString(", ")
		src := e.Source
		if src == "" {
			src = SourceUser
		}
		b.WriteString(src)
		b.WriteString(")\n")
		b.WriteString(e.Content)
		if !strings.HasSuffix(e.Content, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	data := []byte(b.String())
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, data) {
		return nil
	}
	return atomicWrite(path, data)
}

// scopeFromPath extracts the scope hint from a markdown file
// path. It is best-effort: the markdown file is the human
// representation; the SQLite table holds the authoritative
// scope. We only use this to write a sensible "# <title>"
// header.
func scopeFromPath(path string) string {
	base := filepath.Base(path)
	dir := filepath.Base(filepath.Dir(path))
	switch {
	case base == "general.md":
		return "general"
	case strings.HasPrefix(base, "project-") && strings.HasSuffix(base, ".md"):
		return "project:" + strings.TrimSuffix(strings.TrimPrefix(base, "project-"), ".md")
	case strings.HasPrefix(base, "scratch-") && strings.HasSuffix(base, ".md"):
		return "scratch:" + strings.TrimSuffix(strings.TrimPrefix(base, "scratch-"), ".md")
	case dir == "patterns" && strings.HasSuffix(base, ".md"):
		return "pattern:" + strings.TrimSuffix(base, ".md")
	}
	return ""
}

func scopeTitle(scope string) string {
	switch {
	case scope == "general":
		return "general"
	case strings.HasPrefix(scope, "project:"):
		return "project " + strings.TrimPrefix(scope, "project:")
	case strings.HasPrefix(scope, "scratch:"):
		return "scratch " + strings.TrimPrefix(scope, "scratch:")
	case strings.HasPrefix(scope, "pattern:"):
		return "pattern " + strings.TrimPrefix(scope, "pattern:")
	}
	return scope
}

// atomicWrite writes, flushes and atomically replaces through a temporary file
// in the same directory. It deliberately has no in-place fallback: if the
// platform cannot replace the target atomically, the SQLite outbox keeps the
// mirror pending for a later retry instead of risking a truncated mirror.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".mem-*")
	if err != nil {
		return fmt.Errorf("memory.atomicWrite: temp in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("memory.atomicWrite: write: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("memory.atomicWrite: chmod: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("memory.atomicWrite: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("memory.atomicWrite: close: %w", err)
	}
	if err := replaceFile(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("memory.atomicWrite: replace %s: %w", path, err)
	}
	return nil
}
