package library

import (
	"testing"
)

func TestFinder_Check_ExactMatch(t *testing.T) {
	f := NewFinder()
	r := f.Check("leaflet", "10k polygons")
	if !r.Found {
		t.Fatal("expected Found=true for leaflet+10k polygons")
	}
	if r.Alternative != "MapLibre GL" {
		t.Errorf("Alternative = %q, want MapLibre GL", r.Alternative)
	}
	if r.Confidence < 0.9 {
		t.Errorf("Confidence = %f, want >= 0.9", r.Confidence)
	}
}

func TestFinder_Check_CaseInsensitive(t *testing.T) {
	f := NewFinder()
	r := f.Check("Leaflet", "10k polygons")
	if !r.Found {
		t.Fatal("expected Found=true for case-insensitive match")
	}
	if r.Alternative != "MapLibre GL" {
		t.Errorf("Alternative = %q, want MapLibre GL", r.Alternative)
	}
}

func TestFinder_Check_SubstringMatch(t *testing.T) {
	f := NewFinder()
	r := f.Check("moment.js", "formatting dates")
	if !r.Found {
		t.Fatal("expected Found=true for substring match")
	}
	if r.Alternative != "dayjs" {
		t.Errorf("Alternative = %q, want dayjs", r.Alternative)
	}
}

func TestFinder_Check_NoMatch(t *testing.T) {
	f := NewFinder()
	r := f.Check("unknown-lib", "anything")
	if r.Found {
		t.Error("expected Found=false for unknown library")
	}
}

func TestFinder_Check_EmptyLibrary(t *testing.T) {
	f := NewFinder()
	r := f.Check("", "anything")
	if r.Found {
		t.Error("expected Found=false for empty library")
	}
}

func TestFinder_Check_EmptyTask(t *testing.T) {
	f := NewFinder()
	r := f.Check("leaflet", "")
	if r.Found {
		t.Error("expected Found=false for empty task")
	}
}

func TestFinder_Check_GoSQLite(t *testing.T) {
	f := NewFinder()
	r := f.Check("go-sqlite3", "no CGO")
	if !r.Found {
		t.Fatal("expected Found=true for go-sqlite3 + no CGO")
	}
	if r.Alternative != "modernc.org/sqlite" {
		t.Errorf("Alternative = %q, want modernc.org/sqlite", r.Alternative)
	}
}

func TestFinder_Check_LodashES(t *testing.T) {
	f := NewFinder()
	r := f.Check("lodash", "tree-shaking")
	if !r.Found {
		t.Fatal("expected Found=true for lodash + tree-shaking")
	}
	if r.Alternative != "lodash-es" {
		t.Errorf("Alternative = %q, want lodash-es", r.Alternative)
	}
}

func TestFinder_Check_BestScore(t *testing.T) {
	f := NewFinder()
	// "leaflet" has two entries: "10k polygons" and
	// "large dataset". Query "massive dataset" should
	// match "large dataset" better than "10k polygons".
	r := f.Check("leaflet", "massive dataset")
	if !r.Found {
		t.Fatal("expected Found=true")
	}
	if r.Alternative != "MapLibre GL" {
		t.Errorf("Alternative = %q, want MapLibre GL", r.Alternative)
	}
}

func TestFinder_CatalogSize(t *testing.T) {
	f := NewFinder()
	if f.CatalogSize() < 20 {
		t.Errorf("CatalogSize = %d, want >= 20", f.CatalogSize())
	}
}

func TestFinder_Catalog(t *testing.T) {
	f := NewFinder()
	cat := f.Catalog()
	if len(cat) == 0 {
		t.Error("Catalog() returned empty")
	}
	// Verify first entry has all fields set
	if cat[0].Library == "" {
		t.Error("first entry has empty Library")
	}
	if cat[0].Alternative == "" {
		t.Error("first entry has empty Alternative")
	}
}

func TestFormatResult_Found(t *testing.T) {
	r := Result{
		Found:       true,
		Alternative: "dayjs",
		Reason:      "2 KB vs 70 KB",
		Confidence:  0.95,
	}
	txt := FormatResult(r, "moment.js")
	if !contains(txt, "dayjs") {
		t.Errorf("text missing alternative: %s", txt)
	}
	if !contains(txt, "moment.js") {
		t.Errorf("text missing original library: %s", txt)
	}
	if !contains(txt, "95%") {
		t.Errorf("text missing confidence: %s", txt)
	}
}

func TestFormatResult_NotFound(t *testing.T) {
	r := Result{Found: false}
	txt := FormatResult(r, "mylib")
	if !contains(txt, "No known better alternative") {
		t.Errorf("unexpected text: %s", txt)
	}
}

func TestMatchScore_Empty(t *testing.T) {
	if s := matchScore("", "task"); s != 0 {
		t.Errorf("empty query = %f, want 0", s)
	}
	if s := matchScore("q", ""); s != 0 {
		t.Errorf("empty task = %f, want 0", s)
	}
}

func TestMatchScore_Exact(t *testing.T) {
	if s := matchScore("abc", "abc"); s != 1.0 {
		t.Errorf("exact = %f, want 1.0", s)
	}
}

func TestMatchScore_Substring(t *testing.T) {
	s := matchScore("abc", "xyz abc def")
	if s < 0.6 || s > 0.8 {
		t.Errorf("substring = %f, want ~0.7", s)
	}
}

func TestMatchScore_WordOverlap(t *testing.T) {
	s := matchScore("big polygon map", "rendering large polygons on a map")
	if s <= 0 || s >= 0.7 {
		t.Errorf("word overlap = %f, want (0, 0.7)", s)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstr(s, sub))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
