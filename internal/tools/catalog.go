package tools

import (
	"sort"
	"strings"
)

// catalog.go implements F-thin step B1: a compact, non-JSON
// rendering of the tool set for the system prompt.
//
// The full JSON Schema of every visible tool is the dominant
// per-turn input cost (measured baseline ~2,500 tokens for the
// always-on core). For the long tail of tools the model rarely
// needs the schema up front — it needs to know the tool EXISTS
// and roughly how to use it, then pull the full schema on demand.
//
// RenderCatalog produces one line per tool:
//
//	name — first line of description
//
// This is the "lekki katalog zamiast pełnych schematów" from the
// roadmap (TODO chudy protokół, pkt 1). It does not change any
// request path on its own; it is the building block the loop will
// use to replace bulk schema emission for the tool tail.

// CatalogEntry is one tool reduced to its advertisement: the
// name plus a single-line usage hint. It carries no JSON Schema.
type CatalogEntry struct {
	Name string
	Hint string
}

// firstLine returns the first non-empty line of s, trimmed and
// collapsed to a single line. Newlines inside a description would
// break the one-line-per-tool catalog format, so we take only the
// leading sentence-ish chunk.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// truncateHint caps a hint at max runes, appending an ellipsis
// when it had to cut. max <= 0 disables truncation. Operates on
// runes so multibyte (UTF-8) content is never split mid-character.
func truncateHint(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

// CatalogEntries reduces tools to their name+hint advertisements,
// sorted by name for a stable, deterministic catalog (stable order
// is what lets a provider prompt-cache the prefix). hintMax caps
// each hint's length in runes; <= 0 means no cap.
func CatalogEntries(tools []Tool, hintMax int) []CatalogEntry {
	out := make([]CatalogEntry, 0, len(tools))
	for _, t := range tools {
		out = append(out, CatalogEntry{
			Name: t.Name,
			Hint: truncateHint(firstLine(t.Description), hintMax),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// RenderCatalog renders tools as a compact catalog block: one
// "name — hint" line per tool, sorted by name. hintMax caps each
// hint in runes (<= 0 = no cap). Returns "" for an empty tool set
// so callers can omit the block entirely rather than emit a stray
// header.
func RenderCatalog(tools []Tool, hintMax int) string {
	entries := CatalogEntries(tools, hintMax)
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	for i, e := range entries {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(e.Name)
		if e.Hint != "" {
			b.WriteString(" — ")
			b.WriteString(e.Hint)
		}
	}
	return b.String()
}

// EstimateSchemaTokens approximates the input-token cost of
// emitting the full JSON Schema of every tool (name + description
// + schema), the way providers serialise ToolDef today. The
// estimate uses the project-wide chars/4 heuristic (same as
// agent.EstimateVisibleTokens) so catalog savings can be compared
// against the schema baseline without a tokenizer dependency.
func EstimateSchemaTokens(tools []Tool) int {
	chars := 0
	for _, t := range tools {
		chars += len(t.Name) + len(t.Description) + len(t.Schema)
	}
	return chars / 4
}

// EstimateCatalogTokens approximates the input-token cost of the
// compact catalog rendering, using the same chars/4 heuristic as
// EstimateSchemaTokens for an apples-to-apples comparison.
func EstimateCatalogTokens(tools []Tool, hintMax int) int {
	return len(RenderCatalog(tools, hintMax)) / 4
}
