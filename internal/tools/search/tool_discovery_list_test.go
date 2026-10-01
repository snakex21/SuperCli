package search

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func namedListFixture(t *testing.T, indexed bool) *ToolSearcher {
	t.Helper()
	r := NewRegistry()
	for _, name := range []string{"read_many", "search_code", "read_context", "read_lines", "patch_file", "mcp_remote.read", "edit_docx", "ctx_execute", "other_tool"} {
		r.MustRegister(Tool{Name: name, Description: "Read many search code context lines patch file", Schema: `{"type":"object","properties":{"path":{"type":"string"}}}`, Fn: func(context.Context, json.RawMessage) (Result, error) {
			t.Fatal("discovery executed a tool")
			return Result{}, nil
		}})
	}
	var idx *Index
	if indexed {
		var err error
		idx, err = NewInMemoryIndex()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = idx.Close() })
	}
	s := NewToolSearcher(r, idx)
	if indexed {
		if err := s.RebuildIndex(); err != nil {
			t.Fatal(err)
		}
	}
	return s
}
func TestDiscoveryExactNameListsPreserveIntentAndLimits(t *testing.T) {
	for _, indexed := range []bool{false, true} {
		for _, tc := range []struct {
			query string
			limit int
			want  []string
		}{
			{"read_many, search_code, read_context", 0, []string{"read_many", "search_code", "read_context"}},
			{"search_code and patch_file", 0, []string{"search_code", "patch_file"}},
			{"READ_LINES; read_many | mcp_remote.read", 0, []string{"read_lines", "read_many", "mcp_remote.read"}},
			{"read_lines & read_many", 0, []string{"read_lines", "read_many"}},
			{"read_many read_many read_context", 0, []string{"read_many", "read_context"}},
			{"read_many, search_code, read_context", 1, []string{"read_many"}},
			{"read_many search_code read_context read_lines", 0, []string{"read_many", "search_code", "read_context"}},
			{"read_many search_code read_context read_lines patch_file mcp_remote.read edit_docx ctx_execute other_tool", 100, []string{"read_many", "search_code", "read_context", "read_lines", "patch_file", "mcp_remote.read", "edit_docx", "ctx_execute"}},
		} {
			t.Run(fmt.Sprintf("indexed=%v/%s/limit=%d", indexed, tc.query, tc.limit), func(t *testing.T) {
				s := namedListFixture(t, indexed)
				raw, _ := json.Marshal(searchArgs{Query: tc.query, Limit: tc.limit})
				got, err := s.execute(context.Background(), raw)
				if err != nil || got.Err != nil {
					t.Fatalf("%v %v", err, got.Err)
				}
				var result struct {
					Matches []struct{ Name, Schema string }
				}
				if err := json.Unmarshal([]byte(got.Text), &result); err != nil {
					t.Fatal(err)
				}
				var names []string
				for _, match := range result.Matches {
					names = append(names, match.Name)
					spec, ok := s.Registry.Get(match.Name)
					if !ok || spec.Schema != match.Schema || !s.Registry.IsActive(match.Name) {
						t.Fatal("schema or activation lost")
					}
				}
				if !reflect.DeepEqual(names, tc.want) {
					t.Fatalf("got %v, want %v", names, tc.want)
				}
				for _, name := range s.Registry.Names() {
					want := false
					for _, expected := range tc.want {
						want = want || name == expected
					}
					if s.Registry.IsActive(name) != want {
						t.Fatalf("unexpected activation: %s", name)
					}
				}
			})
		}
	}
}
func TestExactNameListLeavesNaturalLanguageAndUnknownNamesToSearch(t *testing.T) {
	s := namedListFixture(t, false)
	for _, query := range []string{"read_many", "find read_lines", "read_lines schema", "read_lines but not patch_file", "read_lines unknown_tool", "and read_lines", "read_lines and", "read_lines and and patch_file", "read_lines tool_search", "read_lines invoke_tool"} {
		if got := exactToolNameListHits(s.Registry, query, 3); got != nil {
			t.Errorf("query %q incorrectly parsed as exact list: %v", query, got)
		}
	}
	for _, name := range s.Registry.Names() {
		if s.Registry.IsActive(name) {
			t.Fatal("list recognition mutated activation")
		}
	}
	if got := exactToolNameListHits(nil, "read_lines read_many", 3); got != nil {
		t.Fatal("nil registry returned a tool")
	}
}

func TestExactNameListRejectsAmbiguousCaseFold(t *testing.T) {
	s := namedListFixture(t, false)
	s.Registry.MustRegister(Tool{Name: "Read_Lines", Description: "A distinct case-sensitive tool", Schema: `{}`, Fn: func(context.Context, json.RawMessage) (Result, error) {
		t.Fatal("discovery executed a tool")
		return Result{}, nil
	}})
	got := exactToolNameListHits(s.Registry, "Read_Lines read_lines", 3)
	if len(got) != 2 || got[0].Name != "Read_Lines" || got[1].Name != "read_lines" {
		t.Fatalf("exact-case identities changed: %v", got)
	}
	for i := 0; i < 200; i++ {
		if got := exactToolNameListHits(s.Registry, "READ_LINES READ_LINES", 3); got != nil {
			t.Fatalf("ambiguous fold guessed identities: %v", got)
		}
	}
	if got := exactToolNameListHits(s.Registry, "read_lines read_lines", 3); len(got) != 1 || got[0].Name != "read_lines" {
		t.Fatalf("exact duplicates changed: %v", got)
	}
}
