package llm

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newSourceDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// nowFuture returns a moment 1 minute in the
// future, used to make probe cache entries
// unambiguously "fresh" relative to test setup.
func nowFuture() time.Time {
	return time.Now().Add(time.Minute)
}

func TestNewCapabilityRegistryFromSources_SeedOnly(t *testing.T) {
	home := t.TempDir()
	db := newSourceDB(t)
	r, err := NewCapabilityRegistryFromSources(home, db)
	if err != nil {
		t.Fatal(err)
	}
	if r.Len() < 5 {
		t.Errorf("seed should provide >5 entries; got %d", r.Len())
	}
	if !r.HasVision("gpt-4o") {
		t.Error("seed should say gpt-4o has vision")
	}
}

func TestNewCapabilityRegistryFromSources_CatalogOverrides(t *testing.T) {
	home := t.TempDir()
	// User downgrades gpt-4o to no-vision in the
	// catalog. The catalog (SourceCatalog) must
	// override the seed (SourceSeed).
	if err := SaveCatalog(home, []ModelInfo{
		{ID: "gpt-4o", Vision: false, Source: SourceCatalog},
	}); err != nil {
		t.Fatal(err)
	}
	r, err := NewCapabilityRegistryFromSources(home, newSourceDB(t))
	if err != nil {
		t.Fatal(err)
	}
	if r.HasVision("gpt-4o") {
		t.Error("catalog must override seed vision=true → vision=false")
	}
}

func TestNewCapabilityRegistryFromSources_ProbeOverrides(t *testing.T) {
	home := t.TempDir()
	db := newSourceDB(t)
	// User marks gpt-4o as not-reasoning in the
	// catalog. A fresh probe result with reasoning
	// must override.
	if err := SaveCatalog(home, []ModelInfo{
		{ID: "gpt-4o", Reasoning: false, Source: SourceCatalog},
	}); err != nil {
		t.Fatal(err)
	}
	if err := SaveProbeCache(db, "gpt-4o", ProbeResult{
		Reasoning: true,
		Vision:    true,
		ProbedAt:  nowFuture(),
	}); err != nil {
		t.Fatal(err)
	}
	r, err := NewCapabilityRegistryFromSources(home, db)
	if err != nil {
		t.Fatal(err)
	}
	if !r.HasReasoning("gpt-4o") {
		t.Error("probe (reasoning=true) must override catalog (reasoning=false)")
	}
}

func TestNewCapabilityRegistryFromSources_NilDB(t *testing.T) {
	home := t.TempDir()
	r, err := NewCapabilityRegistryFromSources(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Seed entries should still be present.
	if r.Len() < 5 {
		t.Errorf("seed entries missing when db=nil; got %d", r.Len())
	}
}

func TestNewCapabilityRegistryFromSources_MalformedCatalog(t *testing.T) {
	home := t.TempDir()
	db := newSourceDB(t)
	// Hand-write a corrupt catalog file. The
	// loader should return an error rather than
	// silently drop the data.
	if err := writeFile(CatalogPath(home), []byte(`{not json`)); err != nil {
		t.Fatal(err)
	}
	_, err := NewCapabilityRegistryFromSources(home, db)
	if err == nil {
		t.Fatal("err = nil, want error for malformed catalog")
	}
}
