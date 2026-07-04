package memory

import (
	"fmt"
	"strings"
	"testing"
)

// seedBriefingStores fills a project + global store with n preferences
// and n task-log (session-journal) entries so budget/order/determinism
// can be exercised deterministically.
func seedBriefingStores(t *testing.T, n int) (global, project *Store, path string) {
	t.Helper()
	global = newStripTestStore(t)
	project = newStripTestStore(t)
	path = `C:\Users\ASRock\Desktop\SuperCli\SuperCli`
	for i := 0; i < n; i++ {
		if err := global.Put(Entry{
			ID:      fmt.Sprintf("pref-%02d", i),
			Scope:   ScopePreference,
			Content: fmt.Sprintf("The user preference number %02d is a durable standing choice.", i),
			Source:  SourceAgent,
		}); err != nil {
			t.Fatal(err)
		}
		if err := project.Put(Entry{
			ID:      fmt.Sprintf("log-%02d", i),
			Scope:   ScopeTaskLog,
			Content: fmt.Sprintf("Session %02d: did work item %02d and touched some files.", i, i),
			Source:  SourceAgent,
		}); err != nil {
			t.Fatal(err)
		}
	}
	RefreshCard(global, path, "test project card", "active")
	return global, project, path
}

// TestBriefingRespectsHardBudget: the injected briefing never exceeds
// the token cap, even when the stores hold far more than fits.
func TestBriefingRespectsHardBudget(t *testing.T) {
	global, project, path := seedBriefingStores(t, 30)
	for _, cap := range []int{120, 200, 300, 700} {
		b := BuildBriefing(global, project, path, cap)
		if got := EstimateTokens(b); got > cap {
			t.Errorf("cap=%d: briefing is %d tokens, over budget:\n%s", cap, got, b)
		}
	}
}

// TestBriefingPreferencesBeforeJournal: user preferences must be packed
// ahead of session-journal (task-log) entries, so under a tight budget
// the identity/preference facts survive and journal lines drop first.
func TestBriefingPreferencesBeforeJournal(t *testing.T) {
	global, project, path := seedBriefingStores(t, 10)
	b := BuildBriefing(global, project, path, 700)
	prefIdx := strings.Index(b, "User preferences:")
	logIdx := strings.Index(b, "Recent sessions:")
	if prefIdx < 0 {
		t.Fatalf("briefing missing preferences section:\n%s", b)
	}
	if logIdx >= 0 && prefIdx > logIdx {
		t.Errorf("preferences must come before the session journal (pref@%d, journal@%d):\n%s", prefIdx, logIdx, b)
	}

	// Under a tight budget the preference block must still be present
	// while the journal is the first thing to be squeezed out.
	tight := BuildBriefing(global, project, path, 120)
	if !strings.Contains(tight, "User preferences:") {
		t.Errorf("tight-budget briefing dropped preferences:\n%s", tight)
	}
}

// TestBriefingDeterministic: the same store snapshot yields byte-identical
// briefings, so the session-start injection is a stable KV-cache prefix.
func TestBriefingDeterministic(t *testing.T) {
	global, project, path := seedBriefingStores(t, 12)
	first := BuildBriefing(global, project, path, 300)
	for i := 0; i < 5; i++ {
		if again := BuildBriefing(global, project, path, 300); again != first {
			t.Fatalf("briefing not deterministic on run %d:\nfirst:\n%s\nagain:\n%s", i, first, again)
		}
	}
}
