package memory

import (
	"context"
	"strings"
	"testing"
)

func newAutoSaverForTest(t *testing.T) (*AutoSaver, *Store, *Store) {
	t.Helper()
	project := newStripTestStore(t)
	global := newStripTestStore(t)
	return &AutoSaver{Project: project, Global: global, ProjectPath: t.TempDir()}, project, global
}

// TestNothingSummaryStillSavesUserFacts is the regression test for
// the "cześć lubię komputery" bug: a session with zero coding work
// (summary = NOTHING) must still persist short user declarations
// as global preference entries.
func TestNothingSummaryStillSavesUserFacts(t *testing.T) {
	saver, project, global := newAutoSaverForTest(t)

	var gotPrompt string
	fake := func(ctx context.Context, prompt string) (string, error) {
		gotPrompt = prompt
		return "NOTHING\nUSER: The user likes computers.", nil
	}
	if !saver.StoreSummary(context.Background(), "user: Cześć lubię komputery\nassistant: Cześć!", fake) {
		t.Fatal("StoreSummary returned false for a successful summarize")
	}

	// The prompt must demand fact extraction even for NOTHING sessions.
	for _, want := range []string{"USER: ", "ALWAYS", "NOTHING"} {
		if !strings.Contains(gotPrompt, want) {
			t.Errorf("summary prompt missing %q:\n%s", want, gotPrompt)
		}
	}
	prefs, err := global.Recent(ScopePreference, 5)
	if err != nil || len(prefs) != 1 {
		t.Fatalf("global preferences = %v, err %v", prefs, err)
	}
	if !strings.Contains(prefs[0].Content, "computers") {
		t.Errorf("preference = %q, want the computers fact", prefs[0].Content)
	}
	// NOTHING itself must not become a task-log entry.
	if logs, _ := project.Recent(ScopeTaskLog, 5); len(logs) != 0 {
		t.Errorf("NOTHING summary stored as task log: %v", logs)
	}
}

// TestStoreRawTailAndSummarizePendingRaw covers the abrupt-close
// path: the raw tail is stored without any LLM call, surfaces in
// the briefing, and the next startup turns it into a task-log
// entry plus global user facts, deleting the raw entry.
func TestStoreRawTailAndSummarizePendingRaw(t *testing.T) {
	saver, project, global := newAutoSaverForTest(t)

	saver.StoreRawTail("user: Cześć lubię komputery\n<thinking>secret</thinking>assistant: Miło mi!")

	raws, err := project.Recent(ScopeRawLog, 5)
	if err != nil || len(raws) != 1 {
		t.Fatalf("raw-log entries = %v, err %v", raws, err)
	}
	if strings.Contains(raws[0].Content, "<thinking>") {
		t.Errorf("raw tail kept reasoning: %q", raws[0].Content)
	}

	// The briefing must surface the raw tail until it is summarized.
	brief := BuildBriefing(global, project, saver.ProjectPath, 700)
	if !strings.Contains(brief, "komputery") {
		t.Errorf("briefing does not show the raw tail:\n%s", brief)
	}

	// Next startup: summarize pending raw entries.
	saver.SummarizePendingRaw(context.Background(), func(ctx context.Context, prompt string) (string, error) {
		if !strings.Contains(prompt, "komputery") {
			t.Errorf("summarize prompt missing raw content:\n%s", prompt)
		}
		return "Small talk in Polish; no files touched.\nUSER: The user likes computers.", nil
	})
	if raws, _ := project.Recent(ScopeRawLog, 5); len(raws) != 0 {
		t.Errorf("raw entry not deleted after summarization: %v", raws)
	}
	if logs, _ := project.Recent(ScopeTaskLog, 5); len(logs) != 1 {
		t.Errorf("task logs after raw summarization = %v", logs)
	}
	prefs, _ := global.Recent(ScopePreference, 5)
	if len(prefs) != 1 || !strings.Contains(prefs[0].Content, "computers") {
		t.Errorf("global preferences after raw summarization = %v", prefs)
	}
}

// TestSummarizePendingRawKeepsEntryOnFailure: a failed summarize
// call must not lose the raw entry.
func TestSummarizePendingRawKeepsEntryOnFailure(t *testing.T) {
	saver, project, _ := newAutoSaverForTest(t)
	saver.StoreRawTail("user: ważna wiadomość")
	saver.SummarizePendingRaw(context.Background(), func(ctx context.Context, prompt string) (string, error) {
		return "", context.DeadlineExceeded
	})
	if raws, _ := project.Recent(ScopeRawLog, 5); len(raws) != 1 {
		t.Errorf("raw entry lost after failed summarize: %v", raws)
	}
}

// TestBriefingHasUsageInstruction: the briefing must tell the model
// to actually use the remembered facts when asked about the user.
func TestBriefingHasUsageInstruction(t *testing.T) {
	saver, project, global := newAutoSaverForTest(t)
	_ = saver
	if err := global.Put(Entry{ID: "pref-1", Scope: ScopePreference, Content: "The user likes computers.", Source: SourceAgent}); err != nil {
		t.Fatal(err)
	}
	brief := BuildBriefing(global, project, "p", 700)
	if !strings.Contains(brief, "asks about") || !strings.Contains(brief, "likes computers") {
		t.Errorf("briefing missing usage instruction or fact:\n%s", brief)
	}
}
