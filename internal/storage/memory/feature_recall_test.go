package memory

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
)

type recallCountingEmbedder struct {
	calls atomic.Int32
	fail  bool
}

func (*recallCountingEmbedder) Name() string { return "recall-fixture" }
func (e *recallCountingEmbedder) Embed(context.Context, string) ([]float32, error) {
	e.calls.Add(1)
	if e.fail {
		return nil, errors.New("offline fixture")
	}
	return []float32{1, 0}, nil
}

func TestRecallSearchFiltersLexicalCandidates(t *testing.T) {
	for _, query := range []string{"needle", "needle\""} { // invalid FTS syntax exercises LIKE fallback
		for _, failedEmbedding := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/embed-fails=%v", query, failedEmbedding), func(t *testing.T) {
				s, err := OpenStore(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				put := func(e Entry) {
					t.Helper()
					if err := s.Put(e); err != nil {
						t.Fatal(err)
					}
				}
				for i := 0; i < 20; i++ {
					id := fmt.Sprintf("noise-%02d", i)
					put(Entry{ID: id, Scope: "pattern:" + id, Content: "needle\" no heuristic matched"})
				}
				put(Entry{ID: "fact", Scope: ScopeFact, Content: "needle\" configuration lives in the portable application folder beside the executable and its assets"})
				put(Entry{ID: "actionable", Scope: "pattern:actionable", Content: "needle\" permission denied: verify the workspace directory and retry with the corrected working directory"})
				var emb *recallCountingEmbedder
				if failedEmbedding {
					emb = &recallCountingEmbedder{fail: true}
					s.SetEmbedder(emb)
				}
				got, err := s.RecallSearch(context.Background(), query, 2)
				if err != nil {
					t.Fatal(err)
				}
				have := map[string]bool{}
				for _, e := range got {
					have[e.ID] = true
				}
				if len(got) != 2 || !have["fact"] || !have["actionable"] {
					t.Fatalf("useful lexical matches lost: %s", idsOf(got))
				}
				if emb != nil && emb.calls.Load() != 1 {
					t.Fatalf("query embedded %d times", emb.calls.Load())
				}
				// Inspection still sees the saved diagnostics; recall never deletes them.
				all, err := s.Search(query, 100)
				if err != nil || len(all) != 22 {
					t.Fatalf("unfiltered search changed: count=%d err=%v", len(all), err)
				}
			})
		}
	}
}

func TestRecallSearchFiltersVectorCandidates(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	entries := []Entry{{ID: "fact", Scope: ScopeFact, Content: "configuration belongs beside the executable"}, {ID: "actionable", Scope: "pattern:permission", Content: "permission denied: correct the workspace directory"}}
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("noise-%02d", i)
		entries = append(entries, Entry{ID: id, Scope: "pattern:" + id, Content: "no heuristic matched"})
	}
	for _, e := range entries {
		if err := s.Put(e); err != nil {
			t.Fatal(err)
		}
	}
	emb := &recallCountingEmbedder{}
	s.SetEmbedder(emb)
	// No background embedding is needed for these deterministic stored vectors.
	for _, e := range entries {
		vector := []float32{1, 0}
		if !IsDiagnosticNoise(e) {
			vector = []float32{0.8, 0.2}
		}
		if _, err := s.db.Exec("INSERT INTO memory_vectors(id,dim,vec) VALUES (?,?,?)", e.ID, len(vector), encodeVec(vector)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.RecallSearch(context.Background(), "semantic-query", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || idsOf(got) != "actionable,fact" {
		t.Fatalf("useful vectors crowded out: %s", idsOf(got))
	}
	if emb.calls.Load() != 1 {
		t.Fatalf("query embedded %d times", emb.calls.Load())
	}
	unfiltered, err := s.HybridSearch(context.Background(), "semantic-query", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(unfiltered) != 2 || !IsDiagnosticNoise(unfiltered[0]) || !IsDiagnosticNoise(unfiltered[1]) {
		t.Fatal("general hybrid search no longer exposes diagnostics for inspection")
	}
}

func TestRecallSearchPolicyMatchesDiagnosticNoise(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	entries := []Entry{
		{ID: "noise", Scope: "pattern:old", Content: "needle no heuristic matched"},
		{ID: "upper-content", Scope: "pattern:upper", Content: "needle NO HEURISTIC MATCHED"},
		{ID: "fact", Scope: ScopeFact, Content: "needle no heuristic matched"},
		{ID: "upper-scope", Scope: "Pattern:old", Content: "needle no heuristic matched"},
		{ID: "useful", Scope: "pattern:fix", Content: "needle use the correct path"},
	}
	want := map[string]bool{}
	for _, e := range entries {
		if err := s.Put(e); err != nil {
			t.Fatal(err)
		}
		if !IsDiagnosticNoise(e) {
			want[e.ID] = true
		}
	}
	got, err := s.RecallSearch(context.Background(), "needle", 10)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, e := range got {
		have[e.ID] = true
	}
	if !reflect.DeepEqual(have, want) {
		t.Fatalf("SQL filtering differs from diagnostic policy: got %v, want %v", have, want)
	}
	recent, err := s.RecallRecent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 2 {
		t.Fatalf("recency fallback must exclude every pattern: %s", idsOf(recent))
	}
	for _, e := range recent {
		if e.ID != "fact" && e.ID != "upper-scope" {
			t.Fatalf("unexpected fallback entry %s", e.ID)
		}
	}
}
