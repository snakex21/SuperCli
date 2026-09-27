package search

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
)

func TestDefaultDiscoveryKeepsDistinctIntentAndExplicitBreadth(t *testing.T) {
	for _, indexed := range []bool{false, true} {
		reg := NewRegistry()
		for _, spec := range []Tool{
			{Name: "patch_source", Description: "patch file"},
			{Name: "context_lines", Description: "file editing"},
			{Name: "word_document", Description: "file"},
			{Name: "calendar_events", Description: "calendar"},
			{Name: "second_patch", Description: "patch file"},
		} {
			spec.Schema = "{}"
			spec.Fn = func(context.Context, json.RawMessage) (Result, error) { return Result{}, nil }
			reg.MustRegister(spec)
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
		searcher := NewToolSearcher(reg, idx)
		if err := func() error {
			if idx == nil {
				return nil
			}
			return searcher.RebuildIndex()
		}(); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			query string
			limit int
			want  []string
		}{
			{"patch file editing", 0, []string{"context_lines", "patch_source", "second_patch"}},
			{"patch file calendar", 0, []string{"patch_source", "second_patch", "calendar_events"}},
			{"patch file", 0, []string{"patch_source", "second_patch"}},
			{"file", 0, nil},
			{"patch file editing", 8, []string{"context_lines", "patch_source", "second_patch", "word_document"}},
			{"word_document", 0, []string{"word_document"}},
		} {
			raw, _ := json.Marshal(map[string]any{"query": tc.query, "limit": tc.limit})
			result, err := searcher.execute(context.Background(), raw)
			if err != nil || result.Err != nil {
				t.Fatalf("%v %+v", err, result)
			}
			var response struct{ Matches []struct{ Name string } }
			if err := json.Unmarshal([]byte(result.Text), &response); err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, m := range response.Matches {
				names = append(names, m.Name)
			}
			if tc.want == nil {
				if len(names) != 3 {
					t.Fatalf("broad query lost alternatives: %v", names)
				}
				continue
			}
			// FTS ties have their own stable ranking; compare membership.
			slices.Sort(names)
			want := slices.Clone(tc.want)
			slices.Sort(want)
			if !slices.Equal(names, want) {
				t.Fatalf("indexed=%t query=%q limit=%d: %v want %v", indexed, tc.query, tc.limit, names, want)
			}
		}
	}
}

func BenchmarkDefaultDiscoveryFocus(b *testing.B) {
	for _, count := range []int{64, 512} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			searcher := discoveryFixture(b, count)
			for _, mode := range []string{"explicit_breadth", "default_focus"} {
				b.Run(mode, func(b *testing.B) {
					limit := 3
					if mode == "default_focus" {
						limit = 0
					}
					raw, _ := json.Marshal(searchArgs{Query: "read project files directory paths", Limit: limit})
					b.ReportAllocs()
					for b.Loop() {
						result, err := searcher.execute(context.Background(), raw)
						if err != nil || result.Err != nil {
							b.Fatalf("%v %+v", err, result)
						}
					}
				})
			}
		})
	}
}
