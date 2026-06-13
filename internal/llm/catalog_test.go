package llm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCatalogPath(t *testing.T) {
	got := CatalogPath(`C:\tools\supercli-data`)
	want := filepath.Join(`C:\tools\supercli-data`, "models.json")
	if got != want {
		t.Errorf("CatalogPath = %q, want %q", got, want)
	}
}

func TestLoadCatalog_Missing(t *testing.T) {
	home := t.TempDir()
	got, err := LoadCatalog(home)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got != nil {
		t.Errorf("got = %v, want nil", got)
	}
}

func TestLoadCatalog_Malformed(t *testing.T) {
	home := t.TempDir()
	path := CatalogPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadCatalog(home)
	if err == nil {
		t.Fatal("err = nil, want error for malformed JSON")
	}
}

func TestLoadCatalog_Valid(t *testing.T) {
	home := t.TempDir()
	raw := []byte(`[
		{"id":"gpt-4o","vision":true,"tool_use":true,"stream":true,"input_cost":2.5,"output_cost":10.0,"context_length":128000},
		{"id":"o1","reasoning":true,"stream":true,"context_length":200000}
	]`)
	if err := os.MkdirAll(filepath.Dir(CatalogPath(home)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(CatalogPath(home), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadCatalog(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	// Sorted by id: gpt-4o first, o1 second.
	if got[0].ID != "gpt-4o" || !got[0].Vision {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1].ID != "o1" || !got[1].Reasoning {
		t.Errorf("got[1] = %+v", got[1])
	}
	// Source forced to SourceCatalog.
	for _, m := range got {
		if m.Source != SourceCatalog {
			t.Errorf("Source = %v, want SourceCatalog", m.Source)
		}
	}
}

func TestLoadCatalog_EmptyIDSkipped(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(CatalogPath(home)), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`[{"id":"","vision":true},{"id":"x","vision":true}]`)
	if err := os.WriteFile(CatalogPath(home), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadCatalog(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "x" {
		t.Errorf("got = %+v, want 1 entry with id=x", got)
	}
}

func TestSaveCatalog_WritesFile(t *testing.T) {
	home := t.TempDir()
	models := []ModelInfo{
		{ID: "b", Vision: true, Source: SourceProbe},
		{ID: "a", Vision: false, ToolUse: true, Source: SourceCatalog},
	}
	if err := SaveCatalog(home, models); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(CatalogPath(home))
	if err != nil {
		t.Fatal(err)
	}
	// File should be valid JSON array.
	var back []ModelInfo
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, data)
	}
	if len(back) != 2 {
		t.Fatalf("len = %d, want 2", len(back))
	}
	// Sorted by id.
	if back[0].ID != "a" || back[1].ID != "b" {
		t.Errorf("order = [%s, %s], want [a, b]", back[0].ID, back[1].ID)
	}
	// Source stripped on the way out.
	for _, m := range back {
		if m.Source != SourceUnknown {
			t.Errorf("Source = %v, want SourceUnknown (stripped)", m.Source)
		}
	}
}

func TestSaveCatalog_AtomicNoTempLeft(t *testing.T) {
	home := t.TempDir()
	if err := SaveCatalog(home, []ModelInfo{{ID: "x", Source: SourceSeed}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(CatalogPath(home) + ".tmp"); !os.IsNotExist(err) {
		t.Errorf(".tmp file should not exist after success")
	}
}

func TestSaveLoadCatalog_RoundTrip(t *testing.T) {
	home := t.TempDir()
	in := []ModelInfo{
		{ID: "gpt-4o", Vision: true, ToolUse: true, Stream: true, InputCost: 2.5, OutputCost: 10, ContextLength: 128000},
		{ID: "o1", Reasoning: true, Stream: true, ContextLength: 200000, LastVerified: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)},
	}
	if err := SaveCatalog(home, in); err != nil {
		t.Fatal(err)
	}
	out, err := LoadCatalog(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	// Compare key fields; Source is reset to SourceCatalog on load.
	for i, m := range in {
		if out[i].ID != m.ID {
			t.Errorf("[%d] ID = %q, want %q", i, out[i].ID, m.ID)
		}
		if out[i].Vision != m.Vision {
			t.Errorf("[%d] Vision = %v, want %v", i, out[i].Vision, m.Vision)
		}
		if out[i].ToolUse != m.ToolUse {
			t.Errorf("[%d] ToolUse = %v, want %v", i, out[i].ToolUse, m.ToolUse)
		}
		if out[i].InputCost != m.InputCost {
			t.Errorf("[%d] InputCost = %v, want %v", i, out[i].InputCost, m.InputCost)
		}
		if out[i].ContextLength != m.ContextLength {
			t.Errorf("[%d] ContextLength = %d, want %d", i, out[i].ContextLength, m.ContextLength)
		}
	}
}

func TestMergeCatalog_NewAdded(t *testing.T) {
	existing := []ModelInfo{{ID: "a", Source: SourceCatalog}}
	fresh := []ModelInfo{{ID: "b", Source: SourceProvider}}
	got := MergeCatalog(existing, fresh)
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Errorf("got = %+v", got)
	}
}

func TestMergeCatalog_SeedCannotOverrideCatalog(t *testing.T) {
	// F16 cardinal rule: a Seed entry MUST NOT
	// overwrite a Catalog entry. The user's hand
	// edits always win.
	existing := []ModelInfo{{ID: "x", Vision: true, Source: SourceCatalog}}
	fresh := []ModelInfo{{ID: "x", Vision: false, Source: SourceSeed}}
	got := MergeCatalog(existing, fresh)
	if len(got) != 1 || !got[0].Vision || got[0].Source != SourceCatalog {
		t.Errorf("got = %+v, want vision=true source=Catalog", got)
	}
}

func TestMergeCatalog_ProbeOverridesCatalog(t *testing.T) {
	existing := []ModelInfo{{ID: "x", Reasoning: false, Source: SourceCatalog}}
	fresh := []ModelInfo{{ID: "x", Reasoning: true, Source: SourceProbe}}
	got := MergeCatalog(existing, fresh)
	if len(got) != 1 || !got[0].Reasoning || got[0].Source != SourceProbe {
		t.Errorf("got = %+v", got)
	}
}

func TestMergeCatalog_FreshestWins(t *testing.T) {
	t1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	existing := []ModelInfo{{ID: "x", Vision: false, Source: SourceCatalog, LastVerified: t2}}
	fresh := []ModelInfo{{ID: "x", Vision: true, Source: SourceCatalog, LastVerified: t1}}
	got := MergeCatalog(existing, fresh)
	// Existing is fresher — keep it.
	if got[0].Vision {
		t.Errorf("existing was fresher, should keep vision=false; got %+v", got[0])
	}
}

func TestMergeCatalog_ZeroLosesToNonZero(t *testing.T) {
	t1 := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	existing := []ModelInfo{{ID: "x", Source: SourceCatalog, LastVerified: t1}}
	fresh := []ModelInfo{{ID: "x", Source: SourceCatalog, LastVerified: time.Time{}}}
	got := MergeCatalog(existing, fresh)
	// Existing has non-zero LastVerified — keep.
	if got[0].LastVerified.IsZero() {
		t.Errorf("should keep non-zero LastVerified; got %+v", got[0])
	}
}

func TestMergeCatalog_SortedResult(t *testing.T) {
	existing := []ModelInfo{{ID: "z", Source: SourceCatalog}}
	fresh := []ModelInfo{{ID: "a", Source: SourceSeed}, {ID: "m", Source: SourceSeed}}
	got := MergeCatalog(existing, fresh)
	want := []string{"a", "m", "z"}
	if len(got) != 3 {
		t.Fatalf("len = %d", len(got))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("[%d] = %q, want %q", i, got[i].ID, id)
		}
	}
}

func TestMergeCatalog_DoesNotMutateInputs(t *testing.T) {
	existing := []ModelInfo{{ID: "x", Source: SourceCatalog}}
	fresh := []ModelInfo{{ID: "x", Source: SourceSeed}}
	_ = MergeCatalog(existing, fresh)
	if existing[0].Source != SourceCatalog {
		t.Errorf("existing mutated: %+v", existing[0])
	}
	if fresh[0].Source != SourceSeed {
		t.Errorf("fresh mutated: %+v", fresh[0])
	}
}

// TestParseCatalogBytes_PreservesUnknowns documents
// the trade-off: the typed API drops unknown
// fields. Use raw JSON for true forward compat.
func TestParseCatalogBytes_DropsUnknowns(t *testing.T) {
	data := []byte(`[{"id":"x","vision":true,"future_field":"hello"}]`)
	got, err := parseCatalogBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "x" {
		t.Fatalf("got = %+v", got)
	}
	// round-trip the file: write then re-read.
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(CatalogPath(home)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(CatalogPath(home), data, 0o644); err != nil {
		t.Fatal(err)
	}
	// The typed loader does not see future_field
	// in the parsed struct, and a typed save will
	// drop it from disk. Verified by counting
	// occurrences of the literal "future_field" in
	// the re-serialized file.
	back, err := LoadCatalog(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveCatalog(home, back); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(CatalogPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), "future_field") {
		t.Errorf("typed SaveCatalog must drop unknown fields; got: %s", written)
	}
}
