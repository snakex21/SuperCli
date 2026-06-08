package tools

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
		Description: "Find tools by name/description. The result includes " +
			"the full JSON schema so you can call the matched tool " +
			"in the same turn. Use this whenever you are not sure a " +
			"tool exists or what its arguments are.",
		Schema: `{
			"type": "object",
			"properties": {
				"query": {"type": "string", "description": "natural-language search, e.g. 'find files by name' or 'search code'"},
				"limit": {"type": "integer", "default": 3, "maximum": 8, "description": "max number of matches to return"}
			},
			"required": ["query"]
		}`,
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
	hits, err := s.Index.Search(a.Query, limit)
	if err != nil {
		return Result{Err: fmt.Errorf("tool_search: index: %w", err)}, nil
	}
	// Build the response: array of {name, server, score,
	// schema}. Activate each match in the registry so the
	// model can call it in the same turn.
	type match struct {
		Name   string  `json:"name"`
		Server string  `json:"server"`
		Score  float64 `json:"score"`
		Schema string  `json:"schema"`
	}
	resp := struct {
		Query   string  `json:"query"`
		Matches []match `json:"matches"`
		Hint    string  `json:"hint"`
	}{
		Query: a.Query,
		Hint:  "You can now call any matched tool by name. The schema above shows the exact arguments.",
	}
	for _, h := range hits {
		tool, ok := s.Registry.Get(h.Name)
		if !ok {
			continue
		}
		s.Registry.Activate(h.Name)
		resp.Matches = append(resp.Matches, match{
			Name:   h.Name,
			Server: h.Server,
			Score:  h.Score,
			Schema: tool.Schema,
		})
	}
	out, err := json.Marshal(resp)
	if err != nil {
		return Result{Err: fmt.Errorf("tool_search: marshal: %w", err)}, nil
	}
	return Result{Text: string(out)}, nil
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
	case "tool_search", "apply_skill", "task", "ask_user", "read_image", "search_code":
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
