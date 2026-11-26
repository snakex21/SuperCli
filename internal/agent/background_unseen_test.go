package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
)

// A worker report saved by an older (detached) run loop while a newer run
// was still writing its answer sits ABOVE that answer in the saved history.
// The next turn must name it as new instead of treating everything above its
// last reply as handled (audited: four reports missed for almost 5 hours).
func TestReportSavedAboveActiveReplyIsNamedInNextWake(t *testing.T) {
	report := "<task-notification>\n<task-id>worker-393</task-id>\n<agent>code</agent>\n<status>completed</status>\n<summary>Zombie spawn rules done</summary>\n<result>ok</result>\n</task-notification>"
	// The older run's loop delivers the report after its own run ended.
	old, err := NewLoop(LoopConfig{Provider: makeScriptedProvider("unused"), Registry: emptyRegistry(), Writer: &recordingWriter{}, MaxSteps: 2})
	if err != nil {
		t.Fatal(err)
	}
	old.Messages = []llm.Message{{Role: llm.RoleUser, Content: "Rozdziel pracę"}, {Role: llm.RoleAssistant, Content: "Workerzy pracują."}}
	old.backgroundDelivery(context.Background())(context.Background(), report)
	refs := old.TakeUnseenBackgroundReports()
	if len(refs) != 1 || refs[0].ID != "worker-393" || refs[0].Status != "completed" {
		t.Fatalf("unseen refs = %+v", refs)
	}
	if old.UnseenBackgroundReports() != 0 || len(old.TakeUnseenBackgroundReports()) != 0 {
		t.Fatal("taking the reports did not acknowledge them")
	}

	for _, wake := range []bool{true, false} {
		provider := makeScriptedProvider("Raport worker-393 przeczytany.")
		next, err := NewLoop(LoopConfig{Provider: provider, Registry: emptyRegistry(), Writer: &recordingWriter{}, MaxSteps: 2})
		if err != nil {
			t.Fatal(err)
		}
		// Saved order: the report landed before the reply the newer run was
		// still writing.
		next.LoadConversation([]llm.Message{
			{Role: llm.RoleUser, Content: "Rozdziel pracę"},
			{Role: llm.RoleAssistant, Content: "Workerzy pracują."},
			{Role: llm.RoleUser, Content: "co z workerami?"},
			{Role: llm.RoleUser, Name: llm.WorkerNotificationName, Content: report, Origin: &llm.ContextOrigin{Source: llm.SourceWorkerNotification}},
			{Role: llm.RoleAssistant, Content: "Nic nowego, czekam na raporty."},
		})
		next.NoteUnseenWorkerReports(refs)
		var events <-chan Event
		if wake {
			events, err = next.RunWake(context.Background(), false)
		} else {
			events, err = next.Run(context.Background(), "i co teraz?")
		}
		if err != nil {
			t.Fatal(err)
		}
		if !drainRun(t, events) {
			t.Fatal("turn did not complete")
		}
		last := provider.reqs[len(provider.reqs)-1]
		if !requestContains(last, "New worker reports since your last reply") || !requestContains(last, "worker-393 (Zombie spawn rules done) — completed") {
			t.Fatalf("wake=%v: the next request does not name the unread report", wake)
		}
		if !requestContains(last, "check the worker's actual status") {
			t.Fatalf("wake=%v: no guidance to verify a worker's status before explaining a missing report", wake)
		}
		// Request-only: the list is not saved and not repeated on later turns.
		if next.unseenReportsHint() != "" {
			t.Fatal("the unread list was not consumed")
		}
		for _, m := range next.Messages {
			if strings.Contains(m.Content, "New worker reports since your last reply") {
				t.Fatal("the request-only list entered the history")
			}
		}
	}
}

// Workers starting together share one "before" fingerprint of a directory
// (each used to scan the workspace on its own).
func TestRecentFingerprintIsSharedBriefly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	first := recentFingerprint(context.Background(), dir)
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("bb"), 0o644); err != nil {
		t.Fatal(err)
	}
	if again := recentFingerprint(context.Background(), dir); again != first {
		t.Fatal("a second worker start within the reuse window scanned again")
	}
	if fresh := workspaceFingerprint(context.Background(), dir); fresh == first {
		t.Fatal("fixture: the change is not visible to a fresh fingerprint")
	}
}
