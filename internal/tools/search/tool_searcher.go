package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"supercli/internal/tools/core"
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
		Name:              "tool_search",
		PreservesEvidence: true,
		Description: "Find a tool, plugin, or MCP capability absent from the current tool set, by name or intent. " +
			"Returns its full schema and activates it. Use already available tools directly.",
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
	} else {
		hits = exactToolNameListHits(s.Registry, a.Query, limit)
	}
	if len(hits) == 0 {
		if download := downloadIntentHit(s.Registry, a.Query); download != nil {
			hits = []SearchResult{*download}
		}
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
		candidates := limit
		if a.Limit <= 0 {
			candidates = max(limit, s.MaxLimit)
		}
		hits = s.lexicalFallback(a.Query, candidates)
		if a.Limit <= 0 {
			hits = s.focusLexicalHits(a.Query, hits)
			if limit > 0 && len(hits) > limit {
				hits = hits[:limit]
			}
		}
	}
	// Build the response: array of {name, server, score,
	// signature, schema}. Activate each match in the registry so
	// the model can call it in the same turn. The signature is a
	// compact one-line call form (cheap for small models); the
	// schema is the exact JSON contract.
	resp := discoveryResponse{
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
		s.Registry.ActivateDiscovered(h.Name)
		resp.Matches = append(resp.Matches, discoveryMatch{
			Name:      h.Name,
			Server:    h.Server,
			Score:     h.Score,
			Signature: toolSignature(tool.Name, tool.Schema),
			Schema:    compactDiscoverySchema(tool.Schema),
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
	text := string(out)
	return Result{Text: text, ModelText: discoveryModelText(resp, text)}, nil
}

// Keep the public response contract stable; only small complete model views use
// an object schema instead of serializing JSON inside another JSON string.
type discoveryMatch struct {
	Name      string  `json:"name"`
	Server    string  `json:"server"`
	Score     float64 `json:"score"`
	Signature string  `json:"signature"`
	Schema    string  `json:"schema"`
}

type discoveryResponse struct {
	Query   string           `json:"query"`
	Matches []discoveryMatch `json:"matches"`
	Hint    string           `json:"hint"`
}

func discoveryModelText(resp discoveryResponse, public string) string {
	if len(public) > core.ModelOutputInlineBytes || len(resp.Matches) == 0 {
		return ""
	}
	type modelMatch struct {
		Name      string  `json:"name"`
		Server    string  `json:"server"`
		Score     float64 `json:"score"`
		Signature string  `json:"signature"`
		Schema    any     `json:"schema"`
	}
	view := struct {
		Query   string       `json:"query"`
		Matches []modelMatch `json:"matches"`
		Hint    string       `json:"hint"`
	}{Query: resp.Query, Hint: resp.Hint, Matches: make([]modelMatch, len(resp.Matches))}
	changed := false
	for i, match := range resp.Matches {
		var schema any = match.Schema
		if json.Valid([]byte(match.Schema)) {
			// RawMessage retains every field and numeric spelling. Empty or
			// malformed schemas keep their original string representation.
			schema = json.RawMessage(match.Schema)
			changed = true
		}
		view.Matches[i] = modelMatch{match.Name, match.Server, match.Score, match.Signature, schema}
	}
	if !changed {
		return ""
	}
	out, err := json.Marshal(view)
	if err != nil || len(out) >= len(public) || len(out) > core.ModelOutputInlineBytes {
		return ""
	}
	return string(out)
}

// Compact only the discovery copy; registry and provider contracts retain their bytes.
func compactDiscoverySchema(schema string) string {
	if schema == "" {
		return schema
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(schema)); err != nil {
		return schema
	}
	return compact.String()
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

// web_fetch mentions downloading only to direct binary/file work to
// web_download. Literal FTS queries such as "downloading assets" can therefore
// match only that advisory text, before lexical synonyms ever get a chance.
// Resolve explicit asset/file download intent to the actual capability; page
// reads, mail attachments and local archive operations keep ordinary ranking.
func downloadIntentHit(reg *Registry, query string) *SearchResult {
	if reg == nil {
		return nil
	}
	download, file := false, false
	for _, word := range lexTokens(query) {
		switch word {
		case "download":
			download = true
		case "file", "files", "asset", "assets", "texture", "textures", "image", "images", "audio", "model", "models", "font", "fonts", "zip", "archive", "archives", "url", "plik", "pliki", "pliku", "plikow", "zasoby", "zasobow", "tekstury":
			file = true
		case "read", "readable", "page", "pages", "article", "articles", "documentation", "instructions", "html", "extract", "unzip", "attachment", "attachments", "mail", "email", "outlook", "thunderbird":
			return nil
		}
	}
	if !download || !file {
		return nil
	}
	if tool, ok := reg.Get("web_download"); ok {
		return &SearchResult{Name: tool.Name, Server: classifyServer(tool.Name), Score: 1}
	}
	return nil
}

// A query consisting only of registered tool names is an explicit list, not
// a bag of description words. Keep its order and bound it by the same limit.
// Unknown words (including negation) retain the existing intent-search path;
// this never resolves a tool outside the caller's current registry.
func exactToolNameListHits(reg *Registry, query string, limit int) []SearchResult {
	fields := strings.FieldsFunc(query, func(r rune) bool {
		return unicode.IsSpace(r) || r == ',' || r == ';' || r == '|' || r == '&'
	})
	if len(fields) < 2 {
		return nil
	}
	hits := make([]SearchResult, 0, min(len(fields), 8))
	seen := make(map[string]bool)
	names := 0
	previousName := false
	for i, field := range fields {
		hit := uniqueListedToolNameHit(reg, field)
		if hit == nil {
			if strings.EqualFold(field, "and") && previousName && i+1 < len(fields) {
				previousName = false
				continue
			}
			return nil
		}
		names++
		previousName = true
		if !seen[hit.Name] {
			seen[hit.Name] = true
			if limit <= 0 || len(hits) < limit {
				hits = append(hits, *hit)
			}
		}
	}
	if names < 2 || !previousName {
		return nil
	}
	return hits
}

// Unlike a single legacy name lookup, a list must not resolve repeated
// case-folded tokens to different identities when a registry contains names
// differing only in case. Exact-case names win; ambiguous folds stay a search.
func uniqueListedToolNameHit(reg *Registry, field string) *SearchResult {
	if reg == nil {
		return nil
	}
	if t, ok := reg.Get(field); ok && t.Name != "tool_search" && t.Name != "invoke_tool" {
		return &SearchResult{Name: t.Name, Server: classifyServer(t.Name), Score: 1}
	}
	lower := strings.ToLower(field)
	var hit *SearchResult
	for _, name := range reg.Names() {
		if name == "tool_search" || name == "invoke_tool" || strings.ToLower(name) != lower {
			continue
		}
		if hit != nil {
			return nil
		}
		hit = &SearchResult{Name: name, Server: classifyServer(name), Score: 1}
	}
	return hit
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
	// Query weights preserve repeated query words without rebuilding a word
	// set for every tool. The per-call seen map deduplicates each tool's text.
	weights := make(map[string]int)
	for _, word := range qTokens {
		weights[word]++
	}
	seen := make(map[string]int, len(weights))
	overlap, document := 0, 0
	countMatch := func(word string) {
		if weight := weights[word]; weight > 0 && seen[word] != document {
			overlap += weight
			seen[word] = document
		}
	}
	var ranked []scored
	for index, name := range s.Registry.Names() {
		// Meta-tools are gateways, never useful search answers.
		if name == "tool_search" || name == "invoke_tool" {
			continue
		}
		t, ok := s.Registry.Get(name)
		if !ok {
			continue
		}
		overlap, document = 0, index+1
		// Name and description were separated by a space, so scanning them
		// separately preserves token boundaries without allocating a joined string.
		forEachLexToken(t.Name, countMatch)
		forEachLexToken(t.Description, countMatch)
		if overlap > 0 {
			ranked = append(ranked, scored{name: name, server: classifyServer(name), score: overlap})
		}
	}
	// Total order preserves deterministic ties without quadratic insertion sort.
	slices.SortFunc(ranked, func(a, b scored) int {
		if a.score > b.score {
			return -1
		}
		if a.score < b.score {
			return 1
		}
		return strings.Compare(a.name, b.name)
	})
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
	forEachLexToken(s, func(word string) { out = append(out, word) })
	return out
}

// Visit the same ASCII words as FieldsFunc after Unicode-aware lowercasing.
// Byte scanning needs no slice of all description words. Non-ASCII bytes are
// delimiters, including invalid UTF-8, just as in the original predicate.
func forEachLexToken(s string, visit func(string)) {
	s = strings.ToLower(s)
	start := 0
	for end := 0; end <= len(s); end++ {
		if end < len(s) && ((s[end] >= 'a' && s[end] <= 'z') || (s[end] >= '0' && s[end] <= '9')) {
			continue
		}
		if end-start >= 3 {
			word := s[start:end]
			switch word {
			case "the", "and", "for", "with", "into", "from", "use", "all":
			// Command discovery must match "run go test build" to descriptions
			// mentioning "builds/tests". Keep unrelated words literal.
			case "tests":
				visit("test")
			case "builds":
				visit("build")
			// Asset downloads should be found from ordinary English/Polish
			// requests without expanding the always-on tool catalog or prompt.
			case "downloads", "downloading", "downloaded", "pobierz", "pobranie", "pobieranie":
				visit("download")
			default:
				visit(word)
			}
		}
		start = end + 1
	}
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

// focusLexicalHits removes a weaker match only when every query word it
// matches is already covered by a stronger result. Ties and complementary
// intents survive. This is only a default lexical-search policy: explicit
// limits and FTS/exact-name results keep their existing behavior.
func (s *ToolSearcher) focusLexicalHits(query string, hits []SearchResult) []SearchResult {
	terms := make(map[string]uint64)
	for _, word := range lexTokens(query) {
		if _, ok := terms[word]; ok {
			continue
		}
		if len(terms) == 64 {
			return hits
		}
		terms[word] = uint64(1) << len(terms)
	}
	if len(terms) < 2 || len(hits) < 2 {
		return hits
	}
	coverage := make([]uint64, len(hits))
	for i, hit := range hits {
		if tool, ok := s.Registry.Get(hit.Name); ok {
			match := func(word string) { coverage[i] |= terms[word] }
			forEachLexToken(tool.Name, match)
			forEachLexToken(tool.Description, match)
		}
	}
	selected := make([]SearchResult, 0, len(hits))
	for i, hit := range hits {
		dominated := false
		if mask := coverage[i]; mask != 0 {
			for _, stronger := range coverage[:i] {
				if stronger != mask && mask&stronger == mask {
					dominated = true
					break
				}
			}
		}
		if !dominated {
			selected = append(selected, hit)
		}
	}
	return selected
}
