package reflect

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"supercli/internal/storage/memory"
)

// Store persists Patterns via the F2.b memory store. One
// Pattern = one memory.Entry with scope "pattern:<id>".
// Markdown + SQLite are both updated so future sessions
// can search patterns via FTS5 (F2.b) and inject the top
// hits (F5.d).
type Store struct {
	// Mem is the F2.b memory store. Required. The store
	// calls Mem.Put/Get/Delete/List/Close; close is NOT
	// called on Shutdown — caller owns it.
	Mem *memory.Store
}

// NewStore wraps a memory.Store. Mem must be non-nil.
func NewStore(mem *memory.Store) (*Store, error) {
	if mem == nil {
		return nil, fmt.Errorf("reflect: nil memory.Store")
	}
	return &Store{Mem: mem}, nil
}

// Save persists p. If p.ID is empty it is filled in with
// HashPattern(Kind, Title) and the change is written
// back to *p so the caller can read the assigned ID.
// The existing entry (same ID) is overwritten — this is
// the natural upsert path.
func (s *Store) Save(ctx context.Context, p *Pattern) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p == nil {
		return fmt.Errorf("reflect: nil pattern")
	}
	if p.ID == "" {
		p.ID = HashPattern(p.Kind, p.Title)
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	entry := s.toEntry(*p)
	if err := s.Mem.Put(entry); err != nil {
		return fmt.Errorf("reflect: store.Save: %w", err)
	}
	return nil
}

// SaveAll persists a batch. On any error the function
// stops and returns; partial saves are not undone (best
// effort — patterns are noise, not data).
func (s *Store) SaveAll(ctx context.Context, ps []Pattern) error {
	for i := range ps {
		if err := s.Save(ctx, &ps[i]); err != nil {
			return err
		}
	}
	return nil
}

// Load fetches a pattern by ID. Returns
// (Pattern{}, false, nil) when not found.
func (s *Store) Load(ctx context.Context, id string) (Pattern, bool, error) {
	if err := ctx.Err(); err != nil {
		return Pattern{}, false, err
	}
	entry, err := s.Mem.Get("pattern:" + id)
	if err != nil {
		return Pattern{}, false, nil // not found is not an error here
	}
	return s.fromEntry(entry), true, nil
}

// List returns all stored patterns, sorted by confidence
// desc. limit <= 0 = no cap.
//
// memory.Store.List takes an exact scope; we need a
// prefix match for "pattern:". We pull the full list
// (no scope) and filter — the count is small enough
// (tens, not thousands) that this is fine.
func (s *Store) List(ctx context.Context, limit int) ([]Pattern, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := s.Mem.List("", -1)
	if err != nil {
		return nil, fmt.Errorf("reflect: store.List: %w", err)
	}
	out := make([]Pattern, 0, len(entries))
	for _, e := range entries {
		if !strings.HasPrefix(e.Scope, "pattern:") {
			continue
		}
		out = append(out, s.fromEntry(e))
	}
	sort.Slice(out, func(i, j int) bool {
		// Higher confidence first; tie-break by ID.
		if out[i].Confidence != out[j].Confidence {
			return out[i].Confidence > out[j].Confidence
		}
		return out[i].ID < out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Delete removes a pattern by ID. Returns (true, nil)
// when the pattern existed and was removed; (false, nil)
// when it was already absent.
func (s *Store) Delete(ctx context.Context, id string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if _, err := s.Mem.Get("pattern:" + id); err != nil {
		return false, nil
	}
	if err := s.Mem.Delete("pattern:" + id); err != nil {
		return false, err
	}
	return true, nil
}

// toEntry maps Pattern -> memory.Entry. The body is
// rendered as a small markdown document so the F2.b
// markdown sync keeps a human-readable file alongside
// the SQLite row. Confidence and Count are stored as
// tags (confidence:0.90, count:5) so the List()
// round-trip can preserve them.
func (s *Store) toEntry(p Pattern) memory.Entry {
	tags := append([]string(nil), p.Tags...)
	if p.Tool != "" {
		tags = append(tags, "tool:"+p.Tool)
	}
	if p.Category != "" {
		tags = append(tags, "category:"+p.Category)
	}
	tags = append(tags, "kind:"+string(p.Kind))
	tags = append(tags, fmt.Sprintf("confidence:%.2f", p.Confidence))
	tags = append(tags, fmt.Sprintf("count:%d", p.Count))
	tags = dedupeLower(tags)

	body := renderMarkdown(p)
	e := memory.Entry{
		ID:      "pattern:" + p.ID,
		Scope:   "pattern:" + p.ID,
		Content: body,
		Source:  sourceFor(p),
		Tags:    tags,
	}
	return e
}

// fromEntry is the inverse of toEntry. Best effort —
// missing fields default to zero values. Count and
// Confidence are recovered from the per-pattern tags.
func (s *Store) fromEntry(e memory.Entry) Pattern {
	id := strings.TrimPrefix(e.Scope, "pattern:")
	return Pattern{
		ID:          id,
		Title:       firstLine(e.Content, "pattern"),
		Kind:        KindFromTags(e.Tags),
		Description: extractDescription(e.Content),
		Body:        e.Content,
		Tags:        e.Tags,
		Confidence:  ConfidenceFromTags(e.Tags),
		Count:       CountFromTags(e.Tags),
		CreatedAt:   e.CreatedAt,
		UpdatedAt:   e.UpdatedAt,
	}
}

// extractDescription pulls the first non-heading, non-
// empty paragraph from the markdown body.
func extractDescription(content string) string {
	lines := strings.Split(content, "\n")
	var b strings.Builder
	inBody := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if inBody {
				// End of paragraph.
				s := strings.TrimSpace(b.String())
				if s != "" {
					return s
				}
			}
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "<!--") {
			continue
		}
		if strings.HasPrefix(trimmed, "**") {
			continue
		}
		inBody = true
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		b.WriteString(trimmed)
	}
	return strings.TrimSpace(b.String())
}

// renderMarkdown produces the markdown body for one
// pattern. The title becomes a heading; the body
// paragraphs are the Description + a metadata block.
func renderMarkdown(p Pattern) string {
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(p.Title)
	b.WriteString("\n\n")
	if p.Description != "" {
		b.WriteString(p.Description)
		b.WriteString("\n\n")
	}
	if p.Example != "" {
		b.WriteString("**Example:** `")
		b.WriteString(p.Example)
		b.WriteString("`\n\n")
	}
	if p.Suggestion != "" {
		b.WriteString("**Suggestion:** ")
		b.WriteString(p.Suggestion)
		b.WriteString("\n\n")
	}
	b.WriteString("<!-- kind=")
	b.WriteString(string(p.Kind))
	b.WriteString(" tool=")
	b.WriteString(p.Tool)
	b.WriteString(" category=")
	b.WriteString(p.Category)
	b.WriteString(" confidence=")
	b.WriteString(fmt.Sprintf("%.2f", p.Confidence))
	b.WriteString(" count=")
	b.WriteString(fmt.Sprintf("%d", p.Count))
	b.WriteString(" -->\n")
	return b.String()
}

// firstLine returns the first non-empty line of content,
// falling back to "pattern" when the content is empty.
// Used as the human label when the entry is round-tripped.
func firstLine(content, fallback string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
		if line != "" {
			return line
		}
	}
	return fallback
}

// sourceFor returns the F2.b memory.Source label for a
// pattern. Pattern entries are agent-derived.
func sourceFor(p Pattern) string { return memory.SourceAgent }

// KindFromTags recovers the Pattern.Kind from the tags
// list. Defaults to KindError.
func KindFromTags(tags []string) Kind {
	for _, t := range tags {
		if strings.HasPrefix(t, "kind:") {
			return Kind(strings.TrimPrefix(t, "kind:"))
		}
	}
	return KindError
}

// ConfidenceFromTags recovers the Confidence from the
// "confidence:N.NN" tag. Returns 0 when missing.
func ConfidenceFromTags(tags []string) float64 {
	for _, t := range tags {
		if strings.HasPrefix(t, "confidence:") {
			var v float64
			_, _ = fmt.Sscanf(strings.TrimPrefix(t, "confidence:"), "%f", &v)
			return v
		}
	}
	return 0
}

// CountFromTags recovers the Count from the "count:N" tag.
// Returns 0 when missing.
func CountFromTags(tags []string) int {
	for _, t := range tags {
		if strings.HasPrefix(t, "count:") {
			var v int
			_, _ = fmt.Sscanf(strings.TrimPrefix(t, "count:"), "%d", &v)
			return v
		}
	}
	return 0
}

// dedupeLower strips duplicates from a string slice,
// case-insensitively, preserving the first occurrence's
// original casing.
func dedupeLower(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		key := strings.ToLower(s)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, s)
	}
	return out
}
