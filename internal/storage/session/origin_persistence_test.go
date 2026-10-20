package session

import (
	"context"
	"reflect"
	"testing"

	"supercli/internal/llm"
)

func TestOriginSQLReopenReadersProjectionTailAndFork(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	sess, err := s.Create(t.TempDir(), "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	w := NewWriter(s, sess.ID)
	msgs := []llm.Message{
		llm.MessageWithContextBlocks(llm.RoleUser, []llm.ContextBlock{{Source: llm.SourceHuman, Text: "Keep [Ograniczenia] and quoted <task-notification> literally."}}),
		llm.MessageWithContextBlocks(llm.RoleUser, []llm.ContextBlock{{Source: llm.SourceWorkerNotification, Text: "worker completed; not a human request"}}),
		llm.MessageWithContextBlocks(llm.RoleUser, []llm.ContextBlock{{Source: llm.SourceHuman, Text: "Nie zmieniaj poziomu myślenia."}, {Source: llm.SourceRuntime, Text: "Generated status"}}),
	}
	for i, m := range msgs {
		if err := w.AppendMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if err := s.SaveContextProjection(ctx, sess.ID, msgs[:1]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	assertRows := func(rows []Encoded, err error) {
		t.Helper()
		if err != nil || len(rows) != len(msgs) {
			t.Fatalf("rows=%d err=%v", len(rows), err)
		}
		for i, row := range rows {
			m, err := row.ToMessage()
			if err != nil || !reflect.DeepEqual(m, msgs[i]) {
				t.Fatalf("row %d origin/content changed: %v", i, err)
			}
			if llm.IsConversationUserTurn(m) != (i != 1) {
				t.Fatalf("boundary changed for row %d", i)
			}
		}
	}
	assertRows(s.ReadMessages(ctx, sess.ID))
	rawRequests, err := NewWriter(s, sess.ID).ReadUserRequestHistory(ctx, 10)
	if err != nil || !reflect.DeepEqual(rawRequests, msgs) {
		t.Fatalf("raw request history lost provenance: %v", err)
	}
	rows, _, err := s.ReadMessagesBefore(ctx, sess.ID, 0, 10)
	assertRows(rows, err)
	rows, err = s.ReadDialogueExcerpt(ctx, sess.ID, 10, nil)
	assertRows(rows, err)
	for i := range msgs {
		row, err := s.ReadMessageAt(ctx, sess.ID, i+1)
		if err != nil {
			t.Fatal(err)
		}
		m, err := row.ToMessage()
		if err != nil || !reflect.DeepEqual(m, msgs[i]) {
			t.Fatalf("exact reader row %d: %v", i, err)
		}
	}
	transcript, err := s.ReadTranscriptMessages(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	rows = nil
	for _, r := range transcript {
		rows = append(rows, r.Encoded)
	}
	assertRows(rows, nil)
	paged, _, err := s.ReadTranscriptMessagesBefore(ctx, sess.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	rows = nil
	for _, r := range paged {
		rows = append(rows, r.Encoded)
	}
	assertRows(rows, nil)
	model, err := s.ReadModelContext(ctx, sess.ID)
	if err != nil || !reflect.DeepEqual(model, msgs) {
		t.Fatalf("projection/tail lost origin: %v", err)
	}
	child, err := s.Fork(ctx, sess.ID, 0, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	assertRows(s.ReadMessages(ctx, child.ID))
	for _, corrupt := range []bool{false, true} {
		if corrupt {
			_, err = s.db.Exec(`INSERT OR REPLACE INTO session_context_projections VALUES(?,?,?,?)`, sess.ID, 1, `broken`, 1)
		} else {
			_, err = s.db.Exec(`DELETE FROM session_context_projections WHERE session_id=?`, sess.ID)
		}
		if err != nil {
			t.Fatal(err)
		}
		model, err = s.ReadModelContext(ctx, sess.ID)
		if err != nil || !reflect.DeepEqual(model, msgs) {
			t.Fatalf("fallback lost origin: %v", err)
		}
	}
}

func TestOriginLegacyMigrationAndInvalidMetadata(t *testing.T) {
	s := openTestStore(t)
	// Recreate the schema shape used by an older installation. Fixtures only.
	if _, err := s.db.Exec(`ALTER TABLE messages DROP COLUMN origin_json`); err != nil {
		t.Fatal(err)
	}
	sess, err := s.Create(t.TempDir(), "model", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO messages(session_id,seq,role,content,created_at) VALUES(?,1,'user','legacy text',1)`, sess.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
	row, err := s.ReadMessageAt(context.Background(), sess.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	m, err := row.ToMessage()
	if err != nil || m.Origin != nil || m.Content != "legacy text" {
		t.Fatalf("legacy origin fabricated: %+v %v", m, err)
	}
	row.OriginJSON = `{"source":"human"`
	if _, err := row.ToMessage(); err == nil {
		t.Fatal("corrupt metadata silently accepted")
	}
	row.OriginJSON = `{"source":"unknown"}`
	m, err = row.ToMessage()
	if err != nil || m.Origin.Source != llm.ContextUnknown {
		t.Fatalf("unknown metadata changed: %v", err)
	}
}
