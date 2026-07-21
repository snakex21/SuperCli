package memory

import (
	"context"
	"strings"
	"testing"
)

// fakeEmbedder maps texts to fixed 3-d vectors so cosine ranking
// is fully deterministic. Unknown texts get a far-away vector.
type fakeEmbedder struct {
	vectors map[string][]float32
}

func (f *fakeEmbedder) Name() string { return "fake" }

func (f *fakeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	if v, ok := f.vectors[text]; ok {
		return v, nil
	}
	return []float32{0, 0, 1}, nil
}

func TestHybridSearch_VectorFindsWhatFTSMisses(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	// "kitty" is semantically close to the query "cat" but shares
	// no keyword with it; "weather" is unrelated on both axes.
	fe := &fakeEmbedder{vectors: map[string][]float32{
		"cat":                       {1, 0, 0},
		"the kitty sleeps all day":  {0.95, 0.1, 0},
		"tomorrow will be sunny":    {0, 1, 0},
		"cats are independent pets": {0.9, 0.2, 0},
	}}
	s.SetEmbedder(fe)

	put := func(id, content string) {
		t.Helper()
		if err := s.Put(Entry{ID: id, Scope: ScopeFact, Content: content, Source: SourceAgent}); err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
	}
	put("m1", "the kitty sleeps all day")
	put("m2", "tomorrow will be sunny")
	put("m3", "cats are independent pets")

	got, err := s.HybridSearch(context.Background(), "cat", 2)
	if err != nil {
		t.Fatalf("hybrid: %v", err)
	}
	ids := idsOf(got)
	if !strings.Contains(ids, "m1") {
		t.Errorf("vector side should surface the kitty entry, got %s", ids)
	}
	if strings.Contains(ids, "m2") {
		t.Errorf("unrelated weather entry should not rank top-2, got %s", ids)
	}
}

func TestHybridSearch_DegradesToFTSWithoutEmbedder(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	if err := s.Put(Entry{ID: "f1", Scope: ScopeFact, Content: "deploy uses docker compose", Source: SourceAgent}); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := s.HybridSearch(context.Background(), "docker", 5)
	if err != nil {
		t.Fatalf("hybrid without embedder: %v", err)
	}
	if len(got) != 1 || got[0].ID != "f1" {
		t.Fatalf("expected FTS fallback hit, got %v", idsOf(got))
	}
	if s.EmbedderName() != "" {
		t.Fatalf("no embedder attached, name should be empty")
	}
}

func TestHybridSearch_DeleteRemovesVector(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	fe := &fakeEmbedder{vectors: map[string][]float32{
		"q":     {1, 0, 0},
		"alpha": {1, 0, 0},
	}}
	s.SetEmbedder(fe)
	if err := s.Put(Entry{ID: "d1", Scope: ScopeFact, Content: "alpha", Source: SourceAgent}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := s.Delete("d1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, err := s.HybridSearch(context.Background(), "q", 5)
	if err != nil {
		t.Fatalf("hybrid: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("deleted entry still returned: %v", idsOf(got))
	}
}

func TestProjectCards_UpsertKeepsDescription(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	if err := s.UpsertProjectCard(ProjectCard{Key: "k1", Name: "proj", Path: "C:/p", Description: "an api server"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Second upsert with empty description must keep the old one.
	if err := s.UpsertProjectCard(ProjectCard{Key: "k1", Name: "proj", Path: "C:/p"}); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	c, ok := s.GetProjectCard("k1")
	if !ok || c.Description != "an api server" {
		t.Fatalf("description lost: %+v ok=%v", c, ok)
	}
}

func TestBuildBriefing_RespectsBudgetAndContent(t *testing.T) {
	dir := t.TempDir()
	global, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("open global: %v", err)
	}
	defer global.Close()
	project, err := OpenProjectStore(dir, "C:/work/demo")
	if err != nil {
		t.Fatalf("open project: %v", err)
	}
	defer project.Close()

	if err := global.Put(Entry{ID: "p1", Scope: ScopePreference, Content: "answers in Polish", Source: SourceAgent}); err != nil {
		t.Fatalf("put pref: %v", err)
	}
	RefreshCard(global, "C:/work/demo", "demo project for tests", "active")
	if err := project.Put(Entry{ID: "l1", Scope: ScopeTaskLog, Content: "added login form", Source: SourceAgent}); err != nil {
		t.Fatalf("put log: %v", err)
	}
	if err := global.Put(Entry{ID: "f1", Scope: ScopeFact, Content: "Użytkownik ma na imię Maks.", Source: SourceAgent}); err != nil {
		t.Fatalf("put fact: %v", err)
	}

	b := BuildBriefing(global, project, "C:/work/demo", 700)
	for _, want := range []string{"answers in Polish", "Użytkownik ma na imię Maks.", "demo project for tests", "added login form"} {
		if !strings.Contains(b, want) {
			t.Errorf("briefing missing %q:\n%s", want, b)
		}
	}
	if estimateTokens(b) > 800 {
		t.Errorf("briefing over budget: %d tokens", estimateTokens(b))
	}
	// Empty stores produce an empty briefing, not a bare header.
	if got := BuildBriefing(nil, nil, "C:/x", 700); got != "" {
		t.Errorf("empty briefing should be \"\", got %q", got)
	}
}

func idsOf(es []Entry) string {
	var ids []string
	for _, e := range es {
		ids = append(ids, e.ID)
	}
	return strings.Join(ids, ",")
}
