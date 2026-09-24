package session

import (
	"context"
	"fmt"
	"testing"
	"time"

	"supercli/internal/llm"
)

func TestReadMessageCountsMatchesDecodedTranscript(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	sess, err := s.Create(t.TempDir(), "fixture", "counts")
	if err != nil {
		t.Fatal(err)
	}
	// Include legacy/malformed rows that cannot be produced by today's writer.
	parts := []string{"", "null", "[]", "[", `[{"Type":"text","Text":"hello"}]`,
		`[{"Type":"text","Text":""}]`, `[{"Type":"image","Image":{"URL":"https://invalid.test/image"}}]`,
		`[{"Type":"image","Image":{}}]`, `[{"Type":"reasoning","Reasoning":{"Text":"scratch"}}]`,
		`[{"Type":"reasoning","Reasoning":null}]`, `[{"Type":"unknown"}]`, `[{"Type":"text","Text":12}]`}
	calls := []string{"", "null", "[]", "[", `[{"ID":"a","Name":"read","Arguments":"{}"},{"ID":"b","Name":"read","Arguments":"legacy"}]`,
		`[{"Name":"read"}]`, `[{"ID":"a"}]`, `[{"ID":23,"Name":"read"}]`}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO messages(session_id,seq,role,content,parts_json,tool_call_id,tool_calls_json,created_at) VALUES(?,?,?,?,?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	seq := 0
	for _, role := range []string{"user", "assistant", "tool", "system", "unknown"} {
		for _, content := range []string{"", " ", "\x00", "Unicode żółw 中文 😀"} {
			for _, part := range parts {
				for _, call := range calls {
					seq++
					id := "call"
					if seq%2 == 0 {
						id = ""
					}
					if _, err := stmt.ExecContext(ctx, sess.ID, seq, role, content, part, id, call, time.Now().UnixNano()); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertParity := func() {
		t.Helper()
		all, err := s.ReadMessages(ctx, sess.ID)
		if err != nil {
			t.Fatal(err)
		}
		want := MessageCounts{}
		for _, row := range all {
			msg, err := row.ToMessage()
			if err != nil {
				continue
			}
			switch msg.Role {
			case llm.RoleUser:
				want.User++
			case llm.RoleAssistant:
				want.Assistant++
				want.ToolCalls += len(msg.ToolCalls)
			case llm.RoleTool:
				want.Tool++
			}
		}
		got, err := s.ReadMessageCounts(ctx, sess.ID)
		if err != nil || got != want {
			t.Fatalf("got=%+v want=%+v err=%v", got, want, err)
		}
	}
	assertParity()
	if _, err := s.TruncateFrom(ctx, sess.ID, seq/2); err != nil {
		t.Fatal(err)
	}
	assertParity()
	other, err := s.Create(t.TempDir(), "fixture", "other")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendMessage(ctx, other.ID, Encoded{Role: "user", Content: "foreign"}); err != nil {
		t.Fatal(err)
	}
	assertParity()
	got, err := s.ReadMessageCounts(ctx, other.ID)
	if err != nil || got != (MessageCounts{User: 1}) {
		t.Fatalf("isolation: %+v %v", got, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.ReadMessageCounts(canceled, sess.ID); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestReadMessageCountsEmptyNullAndLiveChanges(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	sess, err := s.Create(t.TempDir(), "fixture", "counts")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{sess.ID, "missing"} {
		got, err := s.ReadMessageCounts(ctx, id)
		if err != nil || got != (MessageCounts{}) {
			t.Fatalf("empty: %+v %v", got, err)
		}
	}
	for i := 1; i <= 3; i++ {
		if err := s.AppendMessage(ctx, sess.ID, Encoded{Role: "user", Content: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
		got, err := s.ReadMessageCounts(ctx, sess.ID)
		if err != nil || got.User != i {
			t.Fatalf("stale: %+v %v", got, err)
		}
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE messages SET content=NULL WHERE session_id=? AND seq=1", sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadMessages(ctx, sess.ID); err == nil {
		t.Fatal("fixture no longer matches legacy NULL failure")
	}
	if _, err := s.ReadMessageCounts(ctx, sess.ID); err == nil {
		t.Fatal("NULL content scan failure was silently changed")
	}
}
