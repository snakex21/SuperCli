package webgui

import (
	"context"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

// seedTUISession mimics the TUI: EnsureSession creates the row with an empty
// title and the writer only appends messages.
func seedTUISession(t *testing.T, srv *Server, id, prompt string) {
	t.Helper()
	store, err := session.OpenStore(srv.eng.DataDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureSession(id, srv.eng.Home(), "test-model"); err != nil {
		t.Fatal(err)
	}
	if err := session.NewWriter(store, id).AppendMessage(context.Background(), llm.Message{Role: llm.RoleUser, Content: prompt}); err != nil {
		t.Fatal(err)
	}
}

func storedSession(t *testing.T, srv *Server, id string) session.Session {
	t.Helper()
	store, err := session.OpenStore(srv.eng.DataDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	meta, err := store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return meta
}

func TestListSessionsPersistsLocalTitleForUntitledTUISession(t *testing.T) {
	srv := newTestServer(t, false)
	prompt := "Fix the flaky upload test. Then also check retries and logging in detail."
	seedTUISession(t, srv, "tui-untitled", prompt)
	before := storedSession(t, srv, "tui-untitled")
	if before.Title != "" {
		t.Fatalf("fixture title = %q, want empty", before.Title)
	}

	rows, err := srv.eng.listSessions(context.Background(), 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("listSessions = %+v, err=%v", rows, err)
	}
	want := summarizeHistoryMessage(prompt, sessionTitleMaxRunes)
	if want == "" || rows[0].FirstUserMsg != want {
		t.Fatalf("listed title = %q, want %q", rows[0].FirstUserMsg, want)
	}
	after := storedSession(t, srv, "tui-untitled")
	if after.Title != want {
		t.Fatalf("stored title = %q, want %q", after.Title, want)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("local title moved activity: %v -> %v", before.UpdatedAt, after.UpdatedAt)
	}
}

func TestListSessionsKeepsExistingTitle(t *testing.T) {
	srv := newTestServer(t, false)
	id := seedWebSession(t, srv, srv.eng.Home(), "Manual name")
	if _, err := srv.eng.listSessions(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if got := storedSession(t, srv, id).Title; got != "Manual name" {
		t.Fatalf("title = %q, want manual name kept", got)
	}
}

func TestSessionStateResumeNamesUntitledTUISession(t *testing.T) {
	srv := newTestServer(t, false)
	first := "Explain the session store schema"
	seedTUISession(t, srv, "tui-resume", first)

	_, _, sid, err := srv.eng.sessionState(context.Background(), "follow-up question", "tui-resume", srv.eng.Home())
	if err != nil || sid != "tui-resume" {
		t.Fatalf("sessionState sid=%q err=%v", sid, err)
	}
	want := summarizeHistoryMessage(first, sessionTitleMaxRunes)
	if got := storedSession(t, srv, "tui-resume").Title; got != want {
		t.Fatalf("resumed title = %q, want %q (from first prompt)", got, want)
	}
}
