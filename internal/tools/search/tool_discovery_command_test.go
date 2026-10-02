package search_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools/ctxexec"
	"supercli/internal/tools/files"
	"supercli/internal/tools/office"
	"supercli/internal/tools/search"
	"supercli/internal/tools/workflow"
)

func commandDiscoveryFixture(t testing.TB, indexed bool) *search.ToolSearcher {
	t.Helper()
	root := t.TempDir()
	reg := search.NewRegistry()
	for _, tool := range []search.Tool{
		workflow.NewCtxExecuteTool(ctxexec.New(root), root).Spec(),
		office.NewEditDocx(root).Spec(),
		files.NewListDir(root).Spec(),
	} {
		reg.MustRegister(tool)
	}
	reg.MustRegister(search.Tool{Name: "calendar_events", Description: "Read calendar events", Schema: "{}", Fn: func(context.Context, json.RawMessage) (search.Result, error) { return search.Result{}, nil }})
	var idx *search.Index
	if indexed {
		var err error
		idx, err = search.NewInMemoryIndex()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = idx.Close() })
	}
	searcher := search.NewToolSearcher(reg, idx)
	if indexed {
		if err := searcher.RebuildIndex(); err != nil {
			t.Fatal(err)
		}
	}
	return searcher
}

func TestCommandDiscoveryRecognizesBuildAndTestForms(t *testing.T) {
	for _, indexed := range []bool{false, true} {
		for _, tc := range []struct {
			query string
			limit int
			want  []string
		}{
			{"run go test build check", 0, []string{"ctx_execute"}},
			{"run go tests builds check", 0, []string{"ctx_execute"}},
			{"run go test build calendar", 0, []string{"calendar_events", "ctx_execute"}},
			{"run go test build word", 0, []string{"ctx_execute", "edit_docx"}},
			{"run go test build check", 8, []string{"ctx_execute", "edit_docx", "list_dir"}},
			{"run", 0, []string{"ctx_execute", "edit_docx"}},
			{"edit_docx", 0, []string{"edit_docx"}},
			{"ctx_execute", 0, []string{"ctx_execute"}},
		} {
			t.Run(fmt.Sprintf("indexed=%v/%s/limit=%d", indexed, tc.query, tc.limit), func(t *testing.T) {
				s := commandDiscoveryFixture(t, indexed)
				registeredSchemas := make(map[string]string)
				for _, name := range s.Registry.Names() {
					tool, _ := s.Registry.Get(name)
					registeredSchemas[name] = tool.Schema
				}
				args, _ := json.Marshal(map[string]any{"query": tc.query, "limit": tc.limit})
				result, err := s.Spec().Fn(context.Background(), args)
				if err != nil || result.Err != nil {
					t.Fatalf("%v %+v", err, result)
				}
				var response struct {
					Matches []struct{ Name, Schema string }
				}
				if err := json.Unmarshal([]byte(result.Text), &response); err != nil {
					t.Fatal(err)
				}
				var names []string
				for _, hit := range response.Matches {
					names = append(names, hit.Name)
					if !s.Registry.IsActive(hit.Name) {
						t.Fatal("returned tool lost activation")
					}
					var compact bytes.Buffer
					if err := json.Compact(&compact, []byte(registeredSchemas[hit.Name])); err != nil {
						t.Fatalf("fixture schema for %s: %v", hit.Name, err)
					}
					if hit.Schema != compact.String() {
						t.Fatal("returned schema is not the compact discovery copy")
					}
				}
				slices.Sort(names)
				t.Logf("response_bytes=%d estimated_tokens=%d matches=%v", len(result.Text), llm.EstimateMessageTokens(llm.Message{Role: llm.RoleTool, Content: result.Text}), names)
				if !slices.Equal(names, tc.want) {
					t.Fatalf("matches=%v want=%v", names, tc.want)
				}
				for _, name := range s.Registry.Names() {
					tool, _ := s.Registry.Get(name)
					if tool.Schema != registeredSchemas[name] {
						t.Fatalf("discovery changed registered schema: %s", name)
					}
					if s.Registry.IsActive(name) != slices.Contains(tc.want, name) {
						t.Fatalf("unrequested activation: %s", name)
					}
				}
			})
		}
	}
}

func TestCommandDiscoveryKeepsUnrelatedWordsLiteral(t *testing.T) {
	// Exact registered names still take precedence over natural-language folding.
	s := commandDiscoveryFixture(t, false)
	s.Registry.MustRegister(search.Tool{Name: "tests", Description: "An unrelated explicitly named extension", Schema: "{}", Fn: func(context.Context, json.RawMessage) (search.Result, error) { return search.Result{}, nil }})
	res, err := s.Spec().Fn(context.Background(), json.RawMessage(`{"query":"tests"}`))
	if err != nil || res.Err != nil {
		t.Fatalf("%v %+v", err, res)
	}
	var response struct{ Matches []struct{ Name string } }
	if err := json.Unmarshal([]byte(res.Text), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Matches) != 1 || response.Matches[0].Name != "tests" {
		t.Fatalf("exact name lost: %s", res.Text)
	}
}

func BenchmarkCommandDiscoveryForms(b *testing.B) {
	s := commandDiscoveryFixture(b, false)
	raw := json.RawMessage(`{"query":"run go test build check"}`)
	b.ReportAllocs()
	for b.Loop() {
		result, err := s.Spec().Fn(context.Background(), raw)
		if err != nil || result.Err != nil {
			b.Fatalf("%v %+v", err, result)
		}
	}
}
