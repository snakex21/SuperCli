package search

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestIndex_RebuildAndLen(t *testing.T) {
	idx, err := NewInMemoryIndex()
	if err != nil {
		t.Fatalf("NewInMemoryIndex: %v", err)
	}
	defer idx.Close()
	if err := idx.Rebuild([]IndexedTool{
		{Name: "a", Description: "alpha", Schema: `{"type":"object"}`, Server: "core"},
		{Name: "b", Description: "bravo", Schema: `{"type":"object"}`, Server: "core"},
	}); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	n, err := idx.Len()
	if err != nil {
		t.Fatalf("Len: %v", err)
	}
	if n != 2 {
		t.Errorf("Len = %d, want 2", n)
	}
}

func TestIndex_RebuildClearsOldEntries(t *testing.T) {
	idx, _ := NewInMemoryIndex()
	defer idx.Close()
	_ = idx.Rebuild([]IndexedTool{{Name: "a", Description: "alpha"}})
	_ = idx.Rebuild([]IndexedTool{{Name: "b", Description: "bravo"}})
	n, _ := idx.Len()
	if n != 1 {
		t.Errorf("Len = %d, want 1 (old entry should be gone)", n)
	}
}

func TestIndex_SearchBM25Ranking(t *testing.T) {
	idx, _ := NewInMemoryIndex()
	defer idx.Close()
	_ = idx.Rebuild([]IndexedTool{
		{Name: "search_code", Description: "Search for code patterns in the repository using ripgrep or fallback", Schema: `{"properties":{"query":{"type":"string"}}}`},
		{Name: "read_image", Description: "Read an image file from disk and return base64-encoded bytes", Schema: `{"properties":{"path":{"type":"string"}}}`},
		{Name: "ask_user", Description: "Ask the user a multiple-choice question and wait for an answer", Schema: `{"properties":{"question":{"type":"string"}}}`},
	})
	hits, err := idx.Search("search code", 3)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	if hits[0].Name != "search_code" {
		t.Errorf("top hit = %q, want search_code (got %v)", hits[0].Name, hits)
	}
	// Every hit must have a positive score in (0, 1].
	for _, h := range hits {
		if h.Score <= 0 || h.Score > 1 {
			t.Errorf("hit %q score = %f, want 0..1", h.Name, h.Score)
		}
	}
}

func TestIndex_SearchRespectsLimit(t *testing.T) {
	idx, _ := NewInMemoryIndex()
	defer idx.Close()
	tools := []IndexedTool{}
	for i := 0; i < 10; i++ {
		tools = append(tools, IndexedTool{
			Name:        "tool_" + string(rune('a'+i)),
			Description: "search related functionality",
		})
	}
	_ = idx.Rebuild(tools)
	hits, _ := idx.Search("search", 3)
	if len(hits) != 3 {
		t.Errorf("hits = %d, want 3", len(hits))
	}
}

func TestIndex_EmptyQueryRejected(t *testing.T) {
	idx, _ := NewInMemoryIndex()
	defer idx.Close()
	_, err := idx.Search("", 3)
	if err == nil {
		t.Error("expected error for empty query")
	}
	_, err = idx.Search("   ", 3)
	if err == nil {
		t.Error("expected error for whitespace query")
	}
}

func TestIndex_PrefixMatch(t *testing.T) {
	idx, _ := NewInMemoryIndex()
	defer idx.Close()
	_ = idx.Rebuild([]IndexedTool{
		{Name: "search_code", Description: "ripgrep search"},
		{Name: "search_files", Description: "find files by name"},
	})
	// Typing "sea" should match both (prefix wildcard).
	hits, _ := idx.Search("sea", 5)
	if len(hits) < 2 {
		t.Errorf("prefix match failed; got %d hits", len(hits))
	}
}

func TestToolSearcher_Spec(t *testing.T) {
	ts := NewToolSearcher(NewRegistry(), nil)
	spec := ts.Spec()
	if spec.Name != "tool_search" {
		t.Errorf("Name = %q, want tool_search", spec.Name)
	}
	if !strings.Contains(spec.Schema, `"query"`) {
		t.Errorf("schema missing query: %q", spec.Schema)
	}
}

func TestToolSearcher_RebuildIndex(t *testing.T) {
	reg := NewRegistry()
	reg.MustRegister(Tool{
		Name:        "search_code",
		Description: "ripgrep search",
		Schema:      `{}`,
		Fn:          func(_ context.Context, _ json.RawMessage) (Result, error) { return Result{}, nil },
	})
	idx, _ := NewInMemoryIndex()
	defer idx.Close()
	ts := NewToolSearcher(reg, idx)
	if err := ts.RebuildIndex(); err != nil {
		t.Fatalf("RebuildIndex: %v", err)
	}
	n, _ := idx.Len()
	if n != 1 {
		t.Errorf("index len = %d, want 1", n)
	}
}

func TestToolSearcher_Execute_ActivatesMatches(t *testing.T) {
	reg := NewRegistry()
	reg.MustRegister(Tool{
		Name:        "search_code",
		Description: "ripgrep search across the repository",
		Schema:      `{"properties":{"query":{"type":"string"}}}`,
		Fn:          func(_ context.Context, _ json.RawMessage) (Result, error) { return Result{}, nil },
	})
	reg.MustRegister(Tool{
		Name:        "read_image",
		Description: "Read an image file from disk",
		Schema:      `{"properties":{"path":{"type":"string"}}}`,
		Fn:          func(_ context.Context, _ json.RawMessage) (Result, error) { return Result{}, nil },
	})
	idx, _ := NewInMemoryIndex()
	defer idx.Close()
	ts := NewToolSearcher(reg, idx)
	_ = ts.RebuildIndex()
	// tool_search itself is not registered yet, but that's
	// fine — we just call execute() directly via the Fn.
	res, err := ts.execute(context.Background(), json.RawMessage(`{"query":"search","limit":2}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Err != nil {
		t.Fatalf("res.Err: %v", res.Err)
	}
	// Parse the response and verify matches.
	var resp struct {
		Query   string `json:"query"`
		Matches []struct {
			Name   string  `json:"name"`
			Schema string  `json:"schema"`
			Score  float64 `json:"score"`
		} `json:"matches"`
	}
	if err := json.Unmarshal([]byte(res.Text), &resp); err != nil {
		t.Fatalf("unmarshal: %v\ntext=%s", err, res.Text)
	}
	if len(resp.Matches) == 0 {
		t.Errorf("no matches returned")
	}
	// Verify that the matches are now active in the registry.
	for _, m := range resp.Matches {
		if !reg.IsVisible(m.Name) {
			t.Errorf("match %q not activated in registry", m.Name)
		}
	}
}

func TestToolSearcher_Execute_EmptyQuery(t *testing.T) {
	ts := NewToolSearcher(NewRegistry(), &Index{})
	res, _ := ts.execute(context.Background(), json.RawMessage(`{"query":""}`))
	if res.Err == nil {
		t.Error("expected error for empty query")
	}
}

func TestToolSearcher_Execute_LimitClamped(t *testing.T) {
	reg := NewRegistry()
	for i := 0; i < 15; i++ {
		reg.MustRegister(Tool{
			Name:        "t_" + string(rune('a'+i%26)) + "_" + string(rune('a'+i/26)),
			Description: "search related tool " + string(rune('a'+i)),
			Fn:          func(_ context.Context, _ json.RawMessage) (Result, error) { return Result{}, nil },
		})
	}
	idx, _ := NewInMemoryIndex()
	defer idx.Close()
	ts := NewToolSearcher(reg, idx)
	_ = ts.RebuildIndex()
	// Ask for limit=1000; should be clamped to MaxLimit=8.
	res, _ := ts.execute(context.Background(), json.RawMessage(`{"query":"search","limit":1000}`))
	var resp struct {
		Matches []json.RawMessage `json:"matches"`
	}
	_ = json.Unmarshal([]byte(res.Text), &resp)
	if len(resp.Matches) > 8 {
		t.Errorf("matches = %d, want <= 8 (clamped)", len(resp.Matches))
	}
}

// TestToolSearcher_Execute_LexicalFallback verifies that a
// reasonable multi-word phrase the FTS index misses (implicit
// AND) still surfaces the right tool via lexical fallback, and
// that the result carries a non-empty signature so the model can
// call it immediately.
func TestToolSearcher_Execute_LexicalFallback(t *testing.T) {
	reg := NewRegistry()
	reg.MustRegister(Tool{
		Name:        "list_dir",
		Description: "List the entries of a directory. Leave path empty to list the current working directory.",
		Schema:      `{"type":"object","properties":{"path":{"type":"string"}}}`,
		Fn:          func(_ context.Context, _ json.RawMessage) (Result, error) { return Result{}, nil },
	})
	reg.MustRegister(Tool{
		Name:        "read_lines",
		Description: "Read a range of lines from a text file on disk.",
		Schema:      `{"type":"object","properties":{"path":{"type":"string"},"start":{"type":"integer"}},"required":["path"]}`,
		Fn:          func(_ context.Context, _ json.RawMessage) (Result, error) { return Result{}, nil },
	})
	idx, _ := NewInMemoryIndex()
	defer idx.Close()
	ts := NewToolSearcher(reg, idx)
	_ = ts.RebuildIndex()

	// This phrase trips FTS5 implicit-AND (no single tool's text
	// has every word) but lexical overlap should still find
	// list_dir via "list" + "directory".
	res, err := ts.execute(context.Background(), json.RawMessage(`{"query":"list files in a directory folder"}`))
	if err != nil || res.Err != nil {
		t.Fatalf("execute: err=%v resErr=%v", err, res.Err)
	}
	var resp struct {
		Matches []struct {
			Name      string `json:"name"`
			Signature string `json:"signature"`
			Schema    string `json:"schema"`
		} `json:"matches"`
		Hint string `json:"hint"`
	}
	if err := json.Unmarshal([]byte(res.Text), &resp); err != nil {
		t.Fatalf("unmarshal: %v\ntext=%s", err, res.Text)
	}
	var found bool
	for _, m := range resp.Matches {
		if m.Name == "list_dir" {
			found = true
			if m.Signature == "" {
				t.Errorf("list_dir match has empty signature")
			}
			if m.Schema == "" {
				t.Errorf("list_dir match has empty schema")
			}
		}
	}
	if !found {
		t.Fatalf("list_dir not found via lexical fallback; matches=%+v", resp.Matches)
	}
	if !reg.IsVisible("list_dir") {
		t.Errorf("list_dir not activated after fallback match")
	}
	if !strings.Contains(resp.Hint, "callable") {
		t.Errorf("hint should confirm callable tools, got %q", resp.Hint)
	}
}

// TestToolSearcher_Execute_NoMatchHonestHint verifies that a
// query matching nothing returns an empty match list AND an
// honest hint that does NOT claim a tool can now be called.
func TestToolSearcher_Execute_NoMatchHonestHint(t *testing.T) {
	reg := NewRegistry()
	reg.MustRegister(Tool{
		Name:        "list_dir",
		Description: "List the entries of a directory.",
		Schema:      `{"type":"object"}`,
		Fn:          func(_ context.Context, _ json.RawMessage) (Result, error) { return Result{}, nil },
	})
	idx, _ := NewInMemoryIndex()
	defer idx.Close()
	ts := NewToolSearcher(reg, idx)
	_ = ts.RebuildIndex()

	res, err := ts.execute(context.Background(), json.RawMessage(`{"query":"zzqqxx nonexistent gibberish"}`))
	if err != nil || res.Err != nil {
		t.Fatalf("execute: err=%v resErr=%v", err, res.Err)
	}
	var resp struct {
		Matches []json.RawMessage `json:"matches"`
		Hint    string            `json:"hint"`
	}
	if err := json.Unmarshal([]byte(res.Text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Matches) != 0 {
		t.Errorf("expected no matches, got %d", len(resp.Matches))
	}
	if strings.Contains(strings.ToLower(resp.Hint), "now call") ||
		strings.Contains(strings.ToLower(resp.Hint), "schema above") {
		t.Errorf("no-match hint must not claim a callable schema, got %q", resp.Hint)
	}
	if !strings.Contains(strings.ToLower(resp.Hint), "no tool matched") {
		t.Errorf("no-match hint should plainly say no tool matched, got %q", resp.Hint)
	}
}

func TestToolSearcher_Server(t *testing.T) {
	cases := []struct {
		name, want string
	}{
		{"search_code", "core"},
		{"tool_search", "core"},
		{"mcp_semble", "mcp"},
		{"my_custom_tool", "user"},
	}
	for _, c := range cases {
		if got := classifyServer(c.name); got != c.want {
			t.Errorf("classifyServer(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}
