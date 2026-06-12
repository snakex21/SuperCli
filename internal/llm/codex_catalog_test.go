package llm

import "testing"

// A fresh registry must end up with all Codex models registered
// under the given provider name.
func TestRegisterCodexCatalogFresh(t *testing.T) {
	r := NewCapabilityRegistry()
	ids := RegisterCodexCatalog(r, "openai")
	if len(ids) == 0 {
		t.Fatal("no models registered")
	}
	for _, id := range ids {
		mi, ok := r.Get(id)
		if !ok {
			t.Fatalf("model %s not in registry", id)
		}
		if mi.Provider != "openai" {
			t.Fatalf("model %s provider = %q, want openai", id, mi.Provider)
		}
	}
}

// A stale probe-cache row (SourceProbe, empty Provider) must not
// shadow the catalog entry: the provider name has to be forced so
// the /model picker (which filters by configured provider name)
// shows the model.
func TestRegisterCodexCatalogMergesOverProbeCache(t *testing.T) {
	r := NewCapabilityRegistry()
	r.Register(ModelInfo{
		ID:        "gpt-5.5",
		Reasoning: true,
		Stream:    true,
		ToolUse:   true,
		Source:    SourceProbe, // higher priority than SourceCatalog
	})
	RegisterCodexCatalog(r, "codex")
	mi, ok := r.Get("gpt-5.5")
	if !ok {
		t.Fatal("gpt-5.5 missing")
	}
	if mi.Provider != "codex" {
		t.Fatalf("provider = %q, want codex (probe-cache entry shadowed the catalog)", mi.Provider)
	}
	if mi.ContextLength == 0 {
		t.Fatal("context length not backfilled from catalog")
	}
}

// Empty provider name falls back to "codex".
func TestCodexCatalogDefaultName(t *testing.T) {
	for _, m := range CodexCatalog("") {
		if m.Provider != "codex" {
			t.Fatalf("provider = %q, want codex", m.Provider)
		}
	}
}
