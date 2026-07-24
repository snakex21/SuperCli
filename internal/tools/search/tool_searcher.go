package search

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ToolSearcher is a meta-tool exposed to the model. When the
// model wants to find a tool, it calls `tool_search` with a
// natural-language query. The Searcher ranks all registered
// tools and the ToolSearcher activates the top-N in the
// registry, so the model can call them in the same turn.
type ToolSearcher struct {
	Registry *Registry
	Index    *Index
	// DefaultLimit is the cap when the model does not pass
	// an explicit `limit`. Kept low (3) to bound token cost.
	DefaultLimit int
	// MaxLimit is the hard cap on a single search call.
	MaxLimit int
}

// NewToolSearcher builds a ToolSearcher and rebuilds the FTS
// index from the registry's current tools. The index is
// incremental: call Rebuild() after Register() to add new
// tools. The searcher ignores activation — that is the loop's
// job.
func NewToolSearcher(reg *Registry, idx *Index) *ToolSearcher {
	return &ToolSearcher{
		Registry:     reg,
		Index:        idx,
		DefaultLimit: 3,
		MaxLimit:     8,
	}
}

// Spec returns the meta-tool description. It is always
// visible to the model (caller should MarkAlwaysOn).
func (s *ToolSearcher) Spec() Tool {
	return Tool{
		Name: "tool_search",
		Description: "Find a rare tool, plugin, MCP bridge, or optional capability by name/intent. " +
			"Returns its full schema and activates it. " +
			"Do not use for listing directories, reading files, searching code, or editing files — " +
			"use list_dir, read_lines/read_many, search_code, patch_file, or create_file directly. " +
			"Call only when a needed tool is absent from the core set or its arguments are unknown.",
		Schema: `{"type":"object","properties":{
"query":{"type":"string","description":"Natural-language search, e.g. 'find files by name'"},
"limit":{"type":"integer","default":3,"maximum":8}
},"required":["query"]}`,
		Fn: s.execute,
	}
}

// searchArgs is the JSON shape the model sends.
type searchArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

// execute performs the search, activates the top matches, and
// returns their full schemas to the model. The activation is
// additive — the model can keep calling tool_search, and the
// visible set grows.
func (s *ToolSearcher) execute(ctx context.Context, args json.RawMessage) (Result, error) {
	var a searchArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("tool_search: bad args: %w", err)}, nil
	}
	if strings.TrimSpace(a.Query) == "" {
		return Result{Err: fmt.Errorf("tool_search: query is empty")}, nil
	}
	limit := a.Limit
	if limit <= 0 {
		limit = s.DefaultLimit
	}
	if limit > s.MaxLimit {
		limit = s.MaxLimit
	}
	// Exact tool-name query: activate that one tool immediately.
	// Models often call tool_search("read_docx") after seeing a catalog
	// name; FTS/lexical ranking is unnecessary and can dilute the hit.
	var hits []SearchResult
	if exact := exactToolNameHit(s.Registry, a.Query); exact != nil {
		hits = []SearchResult{*exact}
	}
	// Small per-request registries (WebGUI/batch) can skip SQLite entirely
	// and use the deterministic lexical ranker. The long-lived TUI supplies
	// an in-memory FTS index for its much larger catalog.
	if len(hits) == 0 && s.Index != nil {
		var err error
		hits, err = s.Index.Search(a.Query, limit)
		if err != nil {
			// Discovery is a convenience layer. A broken/unavailable index must
			// not strand the model when the registry itself is healthy.
			hits = nil
		}
	}
	// FTS5 uses an implicit AND across query tokens, so a
	// reasonable phrase like "list files in directory" can
	// return nothing if no single tool's text contains every
	// word. When the index comes up empty (or is unavailable),
	// fall back to a lexical token-overlap match so a sane query
	// still surfaces the relevant tool(s).
	if len(hits) == 0 {
		hits = s.lexicalFallback(a.Query, limit)
	}
	// Build the response: array of {name, server, score,
	// signature, schema}. Activate each match in the registry so
	// the model can call it in the same turn. The signature is a
	// compact one-line call form (cheap for small models); the
	// schema is the exact JSON contract.
	type match struct {
		Name      string  `json:"name"`
		Server    string  `json:"server"`
		Score     float64 `json:"score"`
		Signature string  `json:"signature"`
		Schema    string  `json:"schema"`
	}
	resp := struct {
		Query   string  `json:"query"`
		Matches []match `json:"matches"`
		Hint    string  `json:"hint"`
	}{
		Query: a.Query,
	}
	for _, h := range hits {
		if h.Name == "tool_search" || h.Name == "invoke_tool" {
			continue
		}
		tool, ok := s.Registry.Get(h.Name)
		if !ok {
			continue
		}
		s.Registry.Activate(h.Name)
		resp.Matches = append(resp.Matches, match{
			Name:      h.Name,
			Server:    h.Server,
			Score:     h.Score,
			Signature: toolSignature(tool.Name, tool.Schema),
			Schema:    tool.Schema,
		})
	}
	// Truthful hint: only promise a callable schema when we
	// actually returned one. On no match, say so plainly and
	// point the model at the catalog instead of implying it can
	// now call something.
	if len(resp.Matches) > 0 {
		resp.Hint = "These tools are now callable. Each match includes its exact signature and JSON schema; call by name when exposed, or through invoke_tool in the schema-stable toolset."
	} else {
		resp.Hint = "No tool matched this query. Try different keywords, or use the tools already listed in the catalog."
	}
	out, err := json.Marshal(resp)
	if err != nil {
		return Result{Err: fmt.Errorf("tool_search: marshal: %w", err)}, nil
	}
	return Result{Text: string(out)}, nil
}

// exactToolNameHit returns a single perfect hit when the query is exactly
// a registered tool name (case-insensitive). Skips meta-tools.
func exactToolNameHit(reg *Registry, query string) *SearchResult {
	if reg == nil {
		return nil
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return nil
	}
	// Prefer exact case match first (registration is case-sensitive).
	if t, ok := reg.Get(q); ok && t.Name != "tool_search" && t.Name != "invoke_tool" {
		return &SearchResult{Name: t.Name, Server: classifyServer(t.Name), Score: 1.0}
	}
	// Case-insensitive fallback for local models that lower/upper the name.
	low := strings.ToLower(q)
	for _, name := range reg.Names() {
		if name == "tool_search" || name == "invoke_tool" {
			continue
		}
		if strings.ToLower(name) == low {
			return &SearchResult{Name: name, Server: classifyServer(name), Score: 1.0}
		}
	}
	return nil
}

// lexicalFallback ranks registered tools by simple token
// overlap between the query and each tool's name + description.
// It is deliberately dumb and dependency-free so the peek works
// even when the FTS index returns nothing (implicit-AND misses)
// or is otherwise unavailable. Returns up to limit hits sorted
// by descending overlap; tools with zero overlap are excluded.
func (s *ToolSearcher) lexicalFallback(query string, limit int) []SearchResult {
	if s.Registry == nil {
		return nil
	}
	qTokens := lexTokens(query)
	if len(qTokens) == 0 {
		return nil
	}
	type scored struct {
		name   string
		server string
		score  int
	}
	var ranked []scored
	for _, name := range s.Registry.Names() {
		// Meta-tools are gateways, never useful search answers.
		if name == "tool_search" || name == "invoke_tool" {
			continue
		}
		t, ok := s.Registry.Get(name)
		if !ok {
			continue
		}
		haystack := make(map[string]struct{})
		for _, w := range lexTokens(t.Name + " " + t.Description) {
			haystack[w] = struct{}{}
		}
		overlap := 0
		for _, q := range qTokens {
			if _, ok := haystack[q]; ok {
				overlap++
			}
		}
		if overlap > 0 {
			ranked = append(ranked, scored{name: name, server: classifyServer(name), score: overlap})
		}
	}
	// Sort by descending overlap, then name for determinism.
	for i := 1; i < len(ranked); i++ {
		for j := i; j > 0 && (ranked[j].score > ranked[j-1].score ||
			(ranked[j].score == ranked[j-1].score && ranked[j].name < ranked[j-1].name)); j-- {
			ranked[j], ranked[j-1] = ranked[j-1], ranked[j]
		}
	}
	if limit > 0 && len(ranked) > limit {
		ranked = ranked[:limit]
	}
	out := make([]SearchResult, 0, len(ranked))
	for _, r := range ranked {
		// Normalize overlap count to a 0..1 score so the shape
		// matches FTS hits. Cap denominator at len(qTokens).
		out = append(out, SearchResult{
			Name:   r.name,
			Server: r.server,
			Score:  float64(r.score) / float64(len(qTokens)),
		})
	}
	return out
}

// lexTokens lowercases s and splits it into word tokens of 3+
// chars, dropping a few common stopwords that add no signal to
// a tool query. Used only by the lexical fallback.
func lexTokens(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}) {
		if len(f) < 3 {
			continue
		}
		switch f {
		case "the", "and", "for", "with", "into", "from", "use", "all":
			continue
		}
		out = append(out, f)
	}
	return out
}

// RebuildIndex re-indexes every tool in the registry. Call
// this once at startup and after any Register() outside
// startup. Safe to call concurrently with Search().
func (s *ToolSearcher) RebuildIndex() error {
	if s.Registry == nil || s.Index == nil {
		return fmt.Errorf("tool searcher: nil registry or index")
	}
	tools := s.Registry.Names()
	indexed := make([]IndexedTool, 0, len(tools))
	for _, name := range tools {
		// Meta-tools already carry full schema on every route; never index them
		// as discoverable answers (would waste FTS slots / confuse models).
		if name == "tool_search" || name == "invoke_tool" {
			continue
		}
		t, ok := s.Registry.Get(name)
		if !ok {
			continue
		}
		indexed = append(indexed, IndexedTool{
			Name:        t.Name,
			Description: t.Description,
			Schema:      t.Schema,
			Server:      classifyServer(t.Name),
			Tags:        extractTags(t.Description),
		})
	}
	return s.Index.Rebuild(indexed)
}

// classifyServer returns a human label for the tool's
// origin. The default is "core"; future MCP wrappers will
// pass through their server name.
func classifyServer(name string) string {
	switch name {
	case "tool_search", "invoke_tool", "apply_skill", "task", "ask_user", "read_image", "search_code":
		return "core"
	}
	if strings.HasPrefix(name, "mcp_") {
		return "mcp"
	}
	return "user"
}

// extractTags pulls the first 8 unique words longer than 4
// chars from the description. Used as boost hints in the
// FTS5 search via the `tags` column. We don't need them for
// FTS5 to work — bm25 already considers name/description/
// schema — but the column is kept for future expansion
// (synonyms, weights).
func extractTags(desc string) []string {
	seen := make(map[string]struct{}, 8)
	var out []string
	for _, f := range strings.Fields(desc) {
		w := strings.ToLower(strings.Trim(f, ".,;:!?'\""))
		if len(w) < 5 {
			continue
		}
		if _, ok := seen[w]; ok {
			continue
		}
		seen[w] = struct{}{}
		out = append(out, w)
		if len(out) >= 8 {
			break
		}
	}
	return out
}
