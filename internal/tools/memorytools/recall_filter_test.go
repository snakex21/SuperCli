package memorytools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"supercli/internal/storage/memory"
)

func TestRecallFiltersBeforeResultLimit(t *testing.T) {
	for _, scope := range []string{"project", "global"} {
		t.Run(scope, func(t *testing.T) {
			s := openMemStore(t)
			fact := "needle deployment uses a portable folder with configuration and assets kept beside the executable"
			if err := s.Put(memory.Entry{ID: "fact", Scope: memory.ScopeFact, Content: fact, Source: memory.SourceAgent, CreatedAt: time.Unix(1, 0)}); err != nil {
				t.Fatal(err)
			}
			// More patterns than the largest existing recent fallback window (40).
			for i := 0; i < 45; i++ {
				id := fmt.Sprintf("pattern:%02d", i)
				if err := s.Put(memory.Entry{ID: id, Scope: id, Content: "needle no heuristic matched", Source: memory.SourceAgent}); err != nil {
					t.Fatal(err)
				}
			}
			recall := NewRecall(s)
			if scope == "global" {
				recall.Store, recall.Global = nil, s
			}
			for _, query := range []string{"needle", "unmatchedcrosslanguage"} {
				for _, limit := range []int{1, 5, 10} {
					t.Run(fmt.Sprintf("%s/%d", query, limit), func(t *testing.T) {
						args, _ := json.Marshal(map[string]any{"query": query, "scope": scope, "limit": limit})
						result, err := recall.Spec().Fn(context.Background(), args)
						if err != nil || result.Err != nil {
							t.Fatalf("recall: %v / %v", err, result.Err)
						}
						if !strings.Contains(result.Text, fact) || strings.Contains(result.Text, "heuristic") {
							t.Fatalf("saved fact hidden by excluded patterns: %s", result.Text)
						}
						if strings.Contains(result.Text, "cross-language fallback") != (query != "needle") {
							t.Fatalf("wrong search/fallback source: %s", result.Text)
						}
					})
				}
			}
		})
	}
}

// Replay an opt-in, local snapshot; private memory text never belongs in the repo.
func TestRecallSavedMemoryRanking(t *testing.T) {
	path := os.Getenv("SUPERCLI_RECALL_SNAPSHOT")
	if path == "" {
		t.Skip("set SUPERCLI_RECALL_SNAPSHOT to a saved memory snapshot")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name    string
		Queries []string
		Entries []struct{ ID, Scope, Content, Tags, Source string }
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			s := openMemStore(t)
			for _, e := range fixture.Entries {
				if err := s.Put(memory.Entry{ID: e.ID, Scope: e.Scope, Content: e.Content, Tags: memory.EntriesFromCSV(e.Tags), Source: e.Source}); err != nil {
					t.Fatal(err)
				}
			}
			for _, query := range fixture.Queries {
				t.Run(query, func(t *testing.T) {
					all, err := s.Search(query, 10000)
					if err != nil {
						t.Fatal(err)
					}
					var wanted []string
					for _, e := range all {
						if !memory.IsDiagnosticNoise(e) {
							wanted = append(wanted, e.ID)
						}
						if len(wanted) == 5 {
							break
						}
					}
					if len(wanted) == 0 {
						return
					}
					args, _ := json.Marshal(map[string]any{"query": query, "scope": "project", "limit": 5})
					result, err := NewRecall(s).Spec().Fn(context.Background(), args)
					if err != nil || result.Err != nil {
						t.Fatalf("recall: %v / %v", err, result.Err)
					}
					if strings.Contains(result.Text, "cross-language fallback") {
						t.Errorf("existing lexical matches replaced by recent fallback")
					}
					found := 0
					for _, id := range wanted {
						if strings.Contains(result.Text, "- ["+id+"]") {
							found++
						}
					}
					t.Logf("useful matching entries: got %d, want %d", found, len(wanted))
					if found != len(wanted) {
						t.Errorf("useful matches hidden: got %d, want %d", found, len(wanted))
					}
				})
			}
		})
	}
}
