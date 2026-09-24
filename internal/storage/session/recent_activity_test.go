package session

import (
	"context"
	"fmt"
	"testing"
	"time"

	"supercli/internal/llm"
)

func TestRecentSessionsFollowActivityNotCreation(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	first := time.Now().UTC().Add(-96 * time.Hour).Truncate(time.Hour)
	for i, id := range []string{"old", "new", "foreign", "empty"} {
		cwd := "/project"
		if id == "foreign" {
			cwd = "/other"
		}
		if err := s.EnsureSession(id, cwd, "model"); err != nil {
			t.Fatal(err)
		}
		if id != "empty" {
			if err := NewWriter(s, id).AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "first " + id}); err != nil {
				t.Fatal(err)
			}
		}
		stamp := first.Add(time.Duration(i) * 24 * time.Hour).UnixNano()
		if _, err := s.db.Exec("UPDATE messages SET created_at=? WHERE session_id=?", stamp, id); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec("UPDATE sessions SET created_at=?,updated_at=? WHERE id=?", stamp, stamp, id); err != nil {
			t.Fatal(err)
		}
	}
	before, err := s.ListRecentByCwd(ctx, "/project", 1)
	if err != nil || len(before) != 1 || before[0].ID != "new" {
		t.Fatalf("%+v %v", before, err)
	}
	activityStarted := time.Now().UTC()
	if err := NewWriter(s, "old").AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "today"}); err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{"/project", ""} {
		rows, err := s.ListRecentByCwd(ctx, cwd, 1)
		if err != nil || len(rows) != 1 || rows[0].ID != "old" || rows[0].MessageCount != 2 || rows[0].FirstUserMsg != "first old" {
			t.Fatalf("cwd=%q %+v %v", cwd, rows, err)
		}
		if rows[0].UpdatedAt.Before(activityStarted) {
			t.Fatalf("missing activity time: %+v", rows[0])
		}
		if !rows[0].StartedAt.Equal(first) {
			t.Fatalf("start date changed: %+v", rows[0])
		}
	}
}

func BenchmarkRecentProjectSessions(b *testing.B) {
	s, err := OpenStore(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	tx, err := s.db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	sessionStmt, err := tx.Prepare("INSERT INTO sessions(id,cwd,title,model,created_at,updated_at,message_count) VALUES(?,'/project','title','model',?,?,200)")
	if err != nil {
		b.Fatal(err)
	}
	defer sessionStmt.Close()
	messageStmt, err := tx.Prepare("INSERT INTO messages(session_id,seq,role,content,created_at) VALUES(?,?,?,?,?)")
	if err != nil {
		b.Fatal(err)
	}
	defer messageStmt.Close()
	for i := 0; i < 300; i++ {
		id := fmt.Sprintf("session-%03d", i)
		if _, err := sessionStmt.Exec(id, i*200, i*200+199); err != nil {
			b.Fatal(err)
		}
		for seq := 1; seq <= 200; seq++ {
			role := "tool"
			if seq == 1 {
				role = "user"
			}
			if _, err := messageStmt.Exec(id, seq, role, "synthetic message for session list benchmark", i*200+seq); err != nil {
				b.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		rows, err := s.ListRecentByCwd(context.Background(), "/project", 40)
		if err != nil || len(rows) != 40 || rows[0].ID != "session-299" {
			b.Fatalf("rows=%d %v", len(rows), err)
		}
	}
}

func TestRecentSessionsRetainLegacyMessages(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	first := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Hour)
	last := first.Add(24 * time.Hour)
	// Import the shape produced by pre-session-table writers. New databases
	// enforce foreign keys, so disable them only on this fixture connection.
	err := func() error {
		conn, err := s.db.Conn(ctx)
		if err != nil {
			return err
		}
		defer conn.Close()
		if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
			return err
		}
		defer conn.ExecContext(ctx, "PRAGMA foreign_keys=ON")
		for i, content := range []string{"first legacy prompt", "second legacy prompt"} {
			stamp := first.Add(time.Duration(i) * 24 * time.Hour)
			if _, err := conn.ExecContext(ctx,
				"INSERT INTO messages(session_id,seq,role,content,created_at) VALUES('legacy',?,'user',?,?)",
				i+1, content, stamp.UnixNano()); err != nil {
				return err
			}
		}
		return nil
	}()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListRecent(ctx, 10)
	if err != nil || len(rows) != 1 || !rows[0].StartedAt.Equal(first) || !rows[0].UpdatedAt.Equal(last) || rows[0].MessageCount != 2 {
		t.Fatalf("message-only history: %+v %v", rows, err)
	}
	rows, err = s.ListRecentByCwd(ctx, "/project", 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("unknown workspace should not match: %+v %v", rows, err)
	}
	// Old writers may register the session after its messages, leaving the
	// denormalized count at zero. The project view must still include it.
	if err := s.EnsureSession("legacy", "/project", "model"); err != nil {
		t.Fatal(err)
	}
	rows, err = s.ListRecentByCwd(ctx, "/project", 10)
	if err != nil || len(rows) != 1 || rows[0].MessageCount != 2 || rows[0].FirstUserMsg != "first legacy prompt" || !rows[0].StartedAt.Equal(first) {
		t.Fatalf("late session registration: %+v %v", rows, err)
	}
	assertQueryPlanHasNoTempSort(t, s, recentProjectQuery, "/project", 40)
}
