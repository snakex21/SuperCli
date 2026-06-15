package freshness

import (
	"testing"
	"time"
)

func fixedTime(y, m, d int) time.Time {
	return time.Date(y, time.Month(m), d, 12, 0, 0, 0, time.UTC)
}

func TestCheckCatalog_Stale(t *testing.T) {
	now := fixedTime(2026, 6, 7)
	c := &Checker{NowFn: func() time.Time { return now }}
	entries := []CatalogEntry{
		{ID: "gpt-4o", LastVerified: fixedTime(2024, 1, 1)}, // >18 months
		{ID: "gpt-4o-mini", LastVerified: fixedTime(2026, 1, 1)}, // <18 months
	}
	stale := c.CheckCatalog(entries)
	if len(stale) != 1 {
		t.Fatalf("want 1 stale, got %d", len(stale))
	}
	if stale[0].ID != "gpt-4o" {
		t.Errorf("stale ID = %q, want gpt-4o", stale[0].ID)
	}
	if stale[0].Kind != "catalog" {
		t.Errorf("Kind = %q, want catalog", stale[0].Kind)
	}
}

func TestCheckCatalog_ZeroLastVerified_Skipped(t *testing.T) {
	c := &Checker{NowFn: func() time.Time { return fixedTime(2026, 6, 7) }}
	entries := []CatalogEntry{
		{ID: "unknown", LastVerified: time.Time{}},
	}
	stale := c.CheckCatalog(entries)
	if len(stale) != 0 {
		t.Errorf("zero LastVerified should be skipped, got %d", len(stale))
	}
}

func TestCheckCatalog_AllFresh(t *testing.T) {
	c := &Checker{NowFn: func() time.Time { return fixedTime(2026, 6, 7) }}
	entries := []CatalogEntry{
		{ID: "fresh", LastVerified: fixedTime(2026, 5, 1)},
	}
	stale := c.CheckCatalog(entries)
	if len(stale) != 0 {
		t.Errorf("want 0 stale, got %d", len(stale))
	}
}

func TestCheckCatalog_Empty(t *testing.T) {
	c := &Checker{NowFn: func() time.Time { return fixedTime(2026, 6, 7) }}
	stale := c.CheckCatalog(nil)
	if len(stale) != 0 {
		t.Errorf("want 0 stale for nil, got %d", len(stale))
	}
}

func TestCheckSkills_Stale(t *testing.T) {
	now := fixedTime(2026, 6, 7)
	c := &Checker{NowFn: func() time.Time { return now }}
	skills := []SkillEntry{
		{Name: "old-skill", Path: "/x/SKILL.md", Modified: fixedTime(2026, 1, 1)}, // ~5 months > 90 days
		{Name: "new-skill", Path: "/y/SKILL.md", Modified: fixedTime(2026, 5, 1)}, // < 90 days
	}
	stale := c.CheckSkills(skills)
	if len(stale) != 1 {
		t.Fatalf("want 1 stale, got %d", len(stale))
	}
	if stale[0].ID != "old-skill" {
		t.Errorf("stale ID = %q, want old-skill", stale[0].ID)
	}
}

func TestCheckSkills_ZeroModified_Skipped(t *testing.T) {
	c := &Checker{NowFn: func() time.Time { return fixedTime(2026, 6, 7) }}
	skills := []SkillEntry{{Name: "x", Modified: time.Time{}}}
	stale := c.CheckSkills(skills)
	if len(stale) != 0 {
		t.Error("zero Modified should be skipped")
	}
}

func TestCheckPatterns_StalePenalty(t *testing.T) {
	now := fixedTime(2026, 6, 7)
	c := &Checker{NowFn: func() time.Time { return now }}
	patterns := []PatternEntry{
		{ID: "p1", Confidence: 0.9, MostRecent: fixedTime(2026, 1, 1)}, // stale
		{ID: "p2", Confidence: 0.8, MostRecent: fixedTime(2026, 5, 1)}, // fresh
	}
	stale, adjusted := c.CheckPatterns(patterns)
	if len(stale) != 1 {
		t.Fatalf("want 1 stale, got %d", len(stale))
	}
	if stale[0].ID != "p1" {
		t.Errorf("stale ID = %q, want p1", stale[0].ID)
	}
	// Check penalty: 0.9 * 0.3 = 0.27
	if len(adjusted) != 2 {
		t.Fatalf("want 2 adjusted, got %d", len(adjusted))
	}
	var p1 PatternEntry
	for _, p := range adjusted {
		if p.ID == "p1" {
			p1 = p
		}
	}
	if p1.Confidence != 0.27 {
		t.Errorf("p1 confidence = %f, want 0.27 (0.9 * 0.3)", p1.Confidence)
	}
	// p2 should be unchanged
	for _, p := range adjusted {
		if p.ID == "p2" && p.Confidence != 0.8 {
			t.Errorf("p2 confidence changed: %f, want 0.8", p.Confidence)
		}
	}
}

func TestCheckPatterns_FallsBackToCreatedAt(t *testing.T) {
	now := fixedTime(2026, 6, 7)
	c := &Checker{NowFn: func() time.Time { return now }}
	patterns := []PatternEntry{
		{ID: "p1", Confidence: 0.5, CreatedAt: fixedTime(2026, 1, 1), MostRecent: time.Time{}},
	}
	stale, adjusted := c.CheckPatterns(patterns)
	if len(stale) != 1 {
		t.Fatalf("want 1 stale (fallback to CreatedAt), got %d", len(stale))
	}
	if adjusted[0].Confidence != 0.15 {
		t.Errorf("confidence = %f, want 0.15 (0.5 * 0.3)", adjusted[0].Confidence)
	}
}

func TestCheckPatterns_NoDate_KeptAsIs(t *testing.T) {
	c := &Checker{NowFn: func() time.Time { return fixedTime(2026, 6, 7) }}
	patterns := []PatternEntry{
		{ID: "p1", Confidence: 0.7},
	}
	stale, adjusted := c.CheckPatterns(patterns)
	if len(stale) != 0 {
		t.Error("no date = not stale")
	}
	if adjusted[0].Confidence != 0.7 {
		t.Errorf("confidence = %f, want 0.7 unchanged", adjusted[0].Confidence)
	}
}

func TestRunReport_Nil(t *testing.T) {
	c := &Checker{NowFn: func() time.Time { return fixedTime(2026, 6, 7) }}
	r := c.RunReport(nil, nil, nil)
	if r.HasStale() {
		t.Error("nil inputs should produce no stale entries")
	}
}

func TestRunReport_AllSources(t *testing.T) {
	now := fixedTime(2026, 6, 7)
	c := &Checker{NowFn: func() time.Time { return now }}
	r := c.RunReport(
		[]CatalogEntry{{ID: "old", LastVerified: fixedTime(2024, 1, 1)}},
		[]SkillEntry{{Name: "old-skill", Modified: fixedTime(2026, 1, 1)}},
		[]PatternEntry{{ID: "old-pat", Confidence: 0.6, MostRecent: fixedTime(2026, 1, 1)}},
	)
	if !r.HasStale() {
		t.Error("expected HasStale=true")
	}
	if len(r.StaleModels) != 1 {
		t.Errorf("StaleModels = %d, want 1", len(r.StaleModels))
	}
	if len(r.StaleSkills) != 1 {
		t.Errorf("StaleSkills = %d, want 1", len(r.StaleSkills))
	}
	if len(r.StalePatterns) != 1 {
		t.Errorf("StalePatterns = %d, want 1", len(r.StalePatterns))
	}
}

func TestFormatReport_Empty(t *testing.T) {
	r := Report{}
	txt := FormatReport(r)
	if txt != "" {
		t.Errorf("empty report should return empty string, got %q", txt)
	}
}

func TestFormatReport_WithStale(t *testing.T) {
	r := Report{
		StaleModels:  []StaleEntry{{Kind: "catalog", ID: "gpt-4o", Detail: "old"}},
		StaleSkills:  []StaleEntry{{Kind: "skill", ID: "x", Detail: "old skill"}},
		StalePatterns: []StaleEntry{{Kind: "pattern", ID: "p1", Detail: "old pattern"}},
	}
	txt := FormatReport(r)
	if txt == "" {
		t.Fatal("expected non-empty report")
	}
	for _, want := range []string{"Stale models", "Stale skills", "Stale patterns"} {
		if !containsSubstr(txt, want) {
			t.Errorf("report missing %q", want)
		}
	}
}

func TestReport_HasStale(t *testing.T) {
	r := Report{}
	if r.HasStale() {
		t.Error("empty report should not have stale")
	}
	r.StaleModels = []StaleEntry{{}}
	if !r.HasStale() {
		t.Error("report with stale models should have stale")
	}
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestPromptSection_ContainsDateAndStalenessNote(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	out := PromptSection(now)
	if !containsSubstr(out, "current_date: 2026-06-10") {
		t.Errorf("PromptSection missing date: %s", out)
	}
	if !containsSubstr(out, "training data may be stale") {
		t.Errorf("PromptSection missing staleness note: %s", out)
	}
	if !containsSubstr(out, "verify via tools") {
		t.Errorf("PromptSection missing verify-with-tools instruction: %s", out)
	}
}
