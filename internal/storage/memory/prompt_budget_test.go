package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestRecentBudgetedCountsRenderedLabels(t *testing.T) {
	entries := []Entry{
		{ID: strings.Repeat("large-label", 20), Scope: ScopeFact, Content: "x", CreatedAt: time.Unix(2, 0), UpdatedAt: time.Unix(2, 0)},
		{ID: "short", Scope: ScopeFact, Content: "useful fact", CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)},
	}
	s := queryLimitStore(t, entries)
	out, err := s.RecentBudgeted(ScopeFact, 20)
	if err != nil {
		t.Fatal(err)
	}
	if EstimateTokens(out) > 20 || !strings.Contains(out, "useful fact") || strings.Contains(out, "large-label") {
		t.Fatalf("rendered labels escaped the budget or hid a smaller note: %q", out)
	}
}

func TestBriefingSmallBudgetsAreComplete(t *testing.T) {
	global := newStripTestStore(t)
	if err := global.Put(Entry{ID: "preference", Scope: ScopePreference, Content: "Keep application data portable."}); err != nil {
		t.Fatal(err)
	}
	for cap := 1; cap <= 160; cap++ {
		out := BuildBriefing(global, nil, "", cap)
		if EstimateTokens(out) > cap {
			t.Fatalf("cap=%d: got %d tokens", cap, EstimateTokens(out))
		}
		if out != "" && (!strings.HasPrefix(out, "[memory_briefing]\n") || !strings.HasSuffix(out, "[/memory_briefing]") || !strings.Contains(out, "Keep application data portable.")) {
			t.Fatalf("cap=%d: incomplete or content-free briefing: %q", cap, out)
		}
	}
}

func TestBriefingOversizedRecentNoteDoesNotHideFacts(t *testing.T) {
	global := newStripTestStore(t)
	for _, e := range []Entry{
		{ID: "small", Scope: ScopePreference, Content: "Useful standing preference.", CreatedAt: time.Unix(1, 0)},
		{ID: "large", Scope: ScopePreference, Content: strings.Repeat("large diagnostic ", 500), CreatedAt: time.Now()},
	} {
		if err := global.Put(e); err != nil {
			t.Fatal(err)
		}
	}
	out := BuildBriefing(global, nil, "", 120)
	if !strings.Contains(out, "Useful standing preference.") || strings.Contains(out, "large diagnostic") || EstimateTokens(out) > 120 {
		t.Fatalf("oversized note hid useful context: %q", out)
	}
}

func TestRawTailAndPromptTruncationPreserveUTF8(t *testing.T) {
	s := newStripTestStore(t)
	a := &AutoSaver{Project: s}
	a.StoreRawTail(strings.Repeat("ą", 4001) + "x")
	entries, err := s.Recent(ScopeRawLog, 1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("raw tail missing: %v", err)
	}
	if got := entries[0].Content; !utf8.ValidString(got) || len(got) > 8000 || !strings.HasSuffix(got, "x") {
		t.Fatal("raw tail cut through a UTF-8 character")
	}
	for _, n := range []int{1, 3, 4, 79, 80, 600} {
		got := truncate(strings.Repeat("ż", 500), n)
		if !utf8.ValidString(got) || len(got) > n {
			t.Fatalf("invalid truncation at %d bytes", n)
		}
	}
}

func TestPatternEntriesFiltersAndRanksBeforeLimit(t *testing.T) {
	entries := queryLimitEntries(100)
	for i := 0; i < 8; i++ {
		entries = append(entries, Entry{ID: fmt.Sprintf("noise-%d", i), Scope: fmt.Sprintf("pattern:noise-%d", i), Content: "no heuristic matched", Tags: []string{"confidence:1.00"}})
	}
	entries = append(entries,
		Entry{ID: "low", Scope: "pattern:low", Content: "low confidence", Tags: []string{"confidence:0.20"}},
		Entry{ID: "best", Scope: "pattern:best", Content: "use the corrected workspace", Tags: []string{"fixture", "confidence:0.90", "count:7"}},
	)
	s := queryLimitStore(t, entries)
	got, err := s.PatternEntries(context.Background(), 1)
	if err != nil || len(got) != 1 || got[0].ID != "best" {
		t.Fatalf("useful pattern lost before the cap: %v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.PatternEntries(ctx, 1); err == nil {
		t.Fatal("canceled read must not inspect the store")
	}
}

func BenchmarkPatternEntriesMixedStore(b *testing.B) {
	entries := queryLimitEntries(4000)
	for i := 0; i < 50; i++ {
		entries = append(entries, Entry{ID: fmt.Sprintf("pattern-%d", i), Scope: fmt.Sprintf("pattern:%03d", i), Content: "Useful correction for a previous tool error.", Tags: []string{fmt.Sprintf("confidence:%.2f", float64(i)/50)}})
	}
	s := queryLimitStore(b, entries)
	legacy := func() ([]Entry, error) { return legacyPatternEntryRead(s, 3) }
	filtered := func() ([]Entry, error) { return s.PatternEntries(context.Background(), 3) }
	// Verify identical fixture results before timing either implementation.
	old, err := legacy()
	if err != nil {
		b.Fatal(err)
	}
	current, err := filtered()
	if err != nil {
		b.Fatal(err)
	}
	wantIDs := []string{"pattern-49", "pattern-48", "pattern-47"}
	if len(old) != len(wantIDs) || len(current) != len(wantIDs) {
		b.Fatalf("fixture result count differs: legacy=%d filtered=%d", len(old), len(current))
	}
	for i, id := range wantIDs {
		if old[i].ID != id || current[i].ID != id {
			b.Fatalf("fixture result %d differs: legacy=%s filtered=%s want=%s", i, old[i].ID, current[i].ID, id)
		}
	}
	for _, impl := range []struct {
		name string
		read func() ([]Entry, error)
	}{{"legacy", legacy}, {"filtered", filtered}} {
		b.Run(impl.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				got, err := impl.read()
				if err != nil || len(got) != 3 {
					b.Fatalf("pattern read: %v", err)
				}
			}
		})
	}
}

// Baseline from HEAD's reflect.Store.List: read every entry, filter in Go,
// recover confidence once per pattern, sort by confidence/ID, then cap. Both
// subbenchmarks measure Entry retrieval and selection; neither includes the
// common final conversion into reflect.Pattern or prompt rendering.
func legacyPatternEntryRead(s *Store, limit int) ([]Entry, error) {
	entries, err := s.List("", -1)
	if err != nil {
		return nil, err
	}
	type rankedEntry struct {
		entry      Entry
		id         string
		confidence float64
	}
	patterns := make([]rankedEntry, 0, len(entries))
	for _, e := range entries {
		if !strings.HasPrefix(e.Scope, "pattern:") || IsDiagnosticNoise(e) {
			continue
		}
		confidence := 0.0
		for _, tag := range e.Tags {
			if strings.HasPrefix(tag, "confidence:") {
				_, _ = fmt.Sscanf(strings.TrimPrefix(tag, "confidence:"), "%f", &confidence)
				break
			}
		}
		patterns = append(patterns, rankedEntry{entry: e, id: strings.TrimPrefix(e.Scope, "pattern:"), confidence: confidence})
	}
	sort.Slice(patterns, func(i, j int) bool {
		if patterns[i].confidence != patterns[j].confidence {
			return patterns[i].confidence > patterns[j].confidence
		}
		return patterns[i].id < patterns[j].id
	})
	if limit > 0 && len(patterns) > limit {
		patterns = patterns[:limit]
	}
	out := make([]Entry, len(patterns))
	for i, p := range patterns {
		out[i] = p.entry
	}
	return out, nil
}
