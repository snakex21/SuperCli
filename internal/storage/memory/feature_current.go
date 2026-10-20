package memory

import (
	"regexp"
	"strings"
)

// Corrections are explicit, never guessed from a shared topic or recency.
// Tags support exact IDs; quoted obsolete statements support existing notes.
// This is a read view: original notes, files and recall-maintenance APIs survive.
type memoryCorrection struct {
	entry  Entry
	ids    map[string]bool
	quotes []string
}

var obsoleteQuote = regexp.MustCompile("[\"“«`]([^\"”»`\\n]{16,})[\"”»`]\\s+(?i:is (?:outdated|obsolete|no longer valid)|jest nieaktualn[aey]|są nieaktualne)")

func correctionText(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

func (s *Store) memoryCorrections() ([]memoryCorrection, error) {
	// Filter before reading payloads. Journals and unrelated facts never enter
	// this view; ordinary read limits must not hide the replacement note.
	rows, err := s.db.Query(`SELECT id, scope, file_path, line_start, line_end, content, tags, source, created_at, updated_at
 FROM memory_entries WHERE scope IN ('fact','preference','decision') AND
 (tags LIKE '%supersedes:%' OR lower(content) LIKE '%outdated%' OR lower(content) LIKE '%obsolete%'
 OR lower(content) LIKE '%no longer valid%' OR lower(content) LIKE '%nieaktualn%')
 ORDER BY updated_at DESC, created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries, err := scanAll(rows, 0)
	if err != nil {
		return nil, err
	}
	var out []memoryCorrection
	for _, e := range entries {
		c := memoryCorrection{entry: e, ids: map[string]bool{}}
		for _, tag := range e.Tags {
			if id, ok := strings.CutPrefix(tag, "supersedes:"); ok && id != "" && id != e.ID {
				c.ids[id] = true
			}
		}
		for _, match := range obsoleteQuote.FindAllStringSubmatch(e.Content, -1) {
			c.quotes = append(c.quotes, correctionText(match[1]))
		}
		if len(c.ids)+len(c.quotes) > 0 {
			out = append(out, c)
		}
	}
	return out, nil
}

func (c memoryCorrection) replaces(e Entry) bool {
	// UpdatedAt alone can reflect mirror reconciliation of an old note.
	// Require a later authorship time as well, so touching an archive cannot
	// overturn a newer correction or form a replacement cycle.
	if c.entry.ID == e.ID || c.entry.CreatedAt.Before(e.CreatedAt) {
		return false
	}
	// SQLite's existing memory timestamps have second precision. Exact ID
	// relationships can disambiguate two notes written within that second.
	if c.ids[e.ID] {
		return true
	}
	if !c.entry.CreatedAt.After(e.CreatedAt) {
		return false
	}
	text := correctionText(e.Content)
	for _, quote := range c.quotes {
		if strings.Contains(text, quote) {
			return true
		}
	}
	return false
}

func resolveCurrentEntries(entries []Entry, corrections []memoryCorrection) []Entry {
	out := make([]Entry, 0, len(entries))
	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		original := e
		visited := map[string]bool{e.ID: true}
		for step := 0; step < len(corrections); step++ {
			changed := false
			for _, c := range corrections {
				if c.replaces(e) {
					if visited[c.entry.ID] {
						e = original
						break
					}
					e = c.entry
					visited[e.ID] = true
					changed = true
					break
				}
			}
			if !changed {
				break
			}
		}
		if !seen[e.ID] {
			out = append(out, e)
			seen[e.ID] = true
		}
	}
	return out
}

func (s *Store) currentRecall(entries []Entry) ([]Entry, error) {
	if len(entries) == 0 {
		return entries, nil
	}
	corrections, err := s.memoryCorrections()
	if err != nil {
		return nil, err
	}
	return resolveCurrentEntries(entries, corrections), nil
}
