package llm

import "testing"

func TestUniqueSuffixContextLengthTracksCurrentPositiveMetadata(t *testing.T) {
	r := NewCapabilityRegistry()
	if got := r.UniqueSuffixContextLength("model"); got != 0 {
		t.Fatalf("empty catalog = %d", got)
	}
	r.Register(ModelInfo{ID: "provider/Model", ContextLength: 32000})
	r.Register(ModelInfo{ID: "other/model", ContextLength: 0})
	for _, id := range []string{"model", "MODEL", "router/MoDeL"} {
		if got := r.UniqueSuffixContextLength(id); got != 32000 {
			t.Errorf("unique suffix %q = %d", id, got)
		}
	}
	r.Register(ModelInfo{ID: "provider/Model", ContextLength: 64000})
	if got := r.UniqueSuffixContextLength("model"); got != 64000 {
		t.Fatalf("updated context = %d", got)
	}
	r.Register(ModelInfo{ID: "other/model", ContextLength: 64000})
	if got := r.UniqueSuffixContextLength("model"); got != 0 {
		t.Fatalf("two providers remain ambiguous even with the same limit: %d", got)
	}
	r.Register(ModelInfo{ID: "other/model", ContextLength: -1})
	if got := r.UniqueSuffixContextLength("model"); got != 64000 {
		t.Fatalf("nonpositive limit should no longer add ambiguity: %d", got)
	}
	r.RegisterAll([]ModelInfo{{ID: "third/model", ContextLength: 16000, Source: SourceCatalog}})
	if got := r.UniqueSuffixContextLength("model"); got != 0 {
		t.Fatalf("late registration was not observed: %d", got)
	}
	if got := r.UniqueSuffixContextLength("different"); got != 0 {
		t.Fatalf("unknown suffix = %d", got)
	}
}

func TestUniqueSuffixContextLengthUsesWholeSuffixAndUnicodeCaseFolding(t *testing.T) {
	r := NewCapabilityRegistry()
	r.RegisterAll([]ModelInfo{{ID: "provider/ŻÓŁW", ContextLength: 12000}, {ID: "provider/prefix-model", ContextLength: 8000}})
	if got := r.UniqueSuffixContextLength("route/żółw"); got != 12000 {
		t.Fatalf("Unicode alias = %d", got)
	}
	if got := r.UniqueSuffixContextLength("model"); got != 0 {
		t.Fatalf("partial suffix incorrectly accepted: %d", got)
	}
}

func TestUniqueSuffixContextLengthRemainsLiveDuringCatalogRefresh(t *testing.T) {
	r := NewCapabilityRegistry()
	r.Register(ModelInfo{ID: "provider/model", ContextLength: 32000, Source: SourceCatalog})
	start, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		<-start
		for i := 0; i < 1000; i++ {
			r.Register(ModelInfo{ID: "provider/model", ContextLength: 32000 + (i%2)*32000, Source: SourceCatalog})
		}
	}()
	close(start)
	for i := 0; i < 1000; i++ {
		if got := r.UniqueSuffixContextLength("model"); got != 32000 && got != 64000 {
			t.Errorf("unexpected context during catalog refresh: %d", got)
			break
		}
	}
	<-finished
	if got := r.UniqueSuffixContextLength("model"); got != 64000 {
		t.Fatalf("latest catalog limit = %d", got)
	}
}
