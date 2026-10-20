package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCurrentMemoryExplicitCorrectionPreservesArchiveAndUnrelatedFacts(t *testing.T) {
	s := newStripTestStore(t)
	base := time.Unix(1700000000, 0)
	put := func(e Entry) {
		t.Helper()
		if err := s.Put(e); err != nil {
			t.Fatal(err)
		}
	}
	put(Entry{ID: "old", Scope: ScopeFact, Content: "Runner default endpoint is http://localhost:4321.", CreatedAt: base})
	put(Entry{ID: "independent", Scope: ScopeFact, Content: "Runner uses the user's selected reasoning level.", CreatedAt: base})
	put(Entry{ID: "new", Scope: ScopeDecision, Content: `Runner now selects its provider endpoint automatically. Earlier note "default endpoint is http://localhost:4321" is outdated.`, CreatedAt: base.Add(time.Second)})
	before, err := os.ReadFile(filepath.Join(s.root, "memory", "scope-fact.md"))
	if err != nil {
		t.Fatal(err)
	}
	b := BuildBriefing(nil, s, "project", 1000)
	if strings.Contains(b, "- Runner default endpoint") || !strings.Contains(b, "Runner now selects") || !strings.Contains(b, "selected reasoning level") {
		t.Fatalf("incorrect briefing: %s", b)
	}
	if strings.Count(b, "Runner now selects") != 1 {
		t.Fatal("cross-section replacement repeated")
	}
	entries, err := s.RecallSearch(context.Background(), "localhost", 5)
	if err != nil || len(entries) != 1 || entries[0].ID != "new" {
		t.Fatalf("recall resurfaced obsolete note: %+v %v", entries, err)
	}
	archive, err := s.Search("localhost", 5)
	if err != nil || len(archive) != 2 {
		t.Fatalf("archive inspection changed: %v %v", archive, err)
	}
	old, err := s.Get("old")
	if err != nil || old.Content != "Runner default endpoint is http://localhost:4321." {
		t.Fatal("original changed")
	}
	after, err := os.ReadFile(filepath.Join(s.root, "memory", "scope-fact.md"))
	if err != nil || string(before) != string(after) {
		t.Fatal("read view rewrote mirror")
	}
}

func TestCurrentMemoryQuotedPolishCorrectionOutsideBriefingLimit(t *testing.T) {
	s := newStripTestStore(t)
	base := time.Unix(1700000000, 0)
	// A later touch makes the old note recent again; authorship remains older.
	for _, e := range []Entry{
		{ID: "old", Scope: ScopeFact, Content: "Bez parametru idzie na lokalny serwer.", CreatedAt: base},
		{ID: "new", Scope: ScopeFact, Content: `Domyślny adres jest teraz wybierany z providera. Notatka "bez parametru idzie na lokalny serwer" jest nieaktualna.`, CreatedAt: base.Add(time.Second)},
	} {
		if err := s.Put(e); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 10; i++ {
		if err := s.Put(Entry{ID: fmt.Sprint("other-", i), Scope: ScopeFact, Content: fmt.Sprintf("Independent resource %d.", i), CreatedAt: base.Add(2 * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`UPDATE memory_entries SET updated_at=updated_at+100 WHERE id='old'`); err != nil {
		t.Fatal(err)
	}
	b := BuildBriefing(nil, s, "project", 1000)
	if strings.Contains(b, "- Bez parametru") || !strings.Contains(b, "Domyślny adres") {
		t.Fatalf("correction outside recent limit ignored: %s", b)
	}
}

func TestCurrentMemoryIDsChainsIsolationAndSameSecond(t *testing.T) {
	base := time.Unix(1700000000, 0)
	global, project := newStripTestStore(t), newStripTestStore(t)
	for _, s := range []*Store{global, project} {
		if err := s.Put(Entry{ID: "old", Scope: ScopeFact, Content: "Keep independent note in its own store.", CreatedAt: base}); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range []Entry{
		{ID: "middle", Scope: ScopeFact, Content: "Intermediate explicit correction.", Tags: []string{"supersedes:old"}, CreatedAt: base},
		{ID: "new", Scope: ScopeFact, Content: "Final explicit correction.", Tags: []string{"supersedes:middle"}, CreatedAt: base},
	} {
		if err := project.Put(e); err != nil {
			t.Fatal(err)
		}
	}
	b := BuildBriefing(global, project, "project", 1000)
	if !strings.Contains(b, "Keep independent") || !strings.Contains(b, "Final explicit") || strings.Contains(b, "Intermediate explicit") {
		t.Fatalf("scope or chain lost: %s", b)
	}
	entries, err := project.RecallRecent(10)
	if err != nil || len(entries) != 1 || entries[0].ID != "new" {
		t.Fatalf("same second chain: %+v %v", entries, err)
	}
}

func TestCurrentMemoryDoesNotGuessContradictionsOrAcceptCycles(t *testing.T) {
	base := time.Unix(1700000000, 0)
	entries := []Entry{
		{ID: "a", Content: "Prefer verbose answers", CreatedAt: base},
		{ID: "b", Content: "Prefer concise answers", CreatedAt: base.Add(time.Second)},
	}
	corrections := []memoryCorrection{{entry: entries[1], quotes: []string{"unrelated quote"}}}
	if got := resolveCurrentEntries(entries, corrections); len(got) != 2 {
		t.Fatal("semantic guess erased a preference")
	}
	entries[1].CreatedAt = base
	corrections = []memoryCorrection{{entry: entries[0], ids: map[string]bool{"b": true}}, {entry: entries[1], ids: map[string]bool{"a": true}}}
	if got := resolveCurrentEntries(entries, corrections); len(got) != 2 {
		t.Fatalf("cycle discarded evidence: %+v", got)
	}
}

func TestCurrentMemoryKeepsIndependentSimilarDecisions(t *testing.T) {
	s := newStripTestStore(t)
	for i, text := range []string{"Use port 8001 for production service.", "Use port 8002 for production service."} {
		if err := s.Put(Entry{ID: fmt.Sprint("decision-", i), Scope: ScopeDecision, Content: text}); err != nil {
			t.Fatal(err)
		}
	}
	briefing := BuildBriefing(nil, s, "project", 1000)
	if !strings.Contains(briefing, "8001") || !strings.Contains(briefing, "8002") {
		t.Fatalf("independent decision lost: %s", briefing)
	}
}
