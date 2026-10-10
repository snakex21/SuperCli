package session

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func boundSummaryFixture(t *testing.T) (*Store, TurnSummary) {
	t.Helper()
	s := openTestStore(t)
	sess := receiptSession(t, s)
	r, err := s.AppendMessageWithReceipt(context.Background(), sess.ID, Encoded{Role: "user", Content: "original"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendMessage(context.Background(), sess.ID, Encoded{Role: "assistant", Content: "response"}); err != nil {
		t.Fatal(err)
	}
	return s, TurnSummary{SessionID: sess.ID, AssistantSeq: 2, Input: 17, Output: 5, DurationMS: 300, ToolCalls: 1,
		Phases: map[string]int64{"model": 240}, ToolDiag: TurnToolDiag{NoOpSearches: 1},
		CheckpointBinding: &CheckpointBinding{Key: strings.Repeat("a", 32), UserSeq: r.Seq, UserMessageID: r.ID}}
}

func readBoundSummary(t *testing.T, s *Store, sid string) TurnSummary {
	t.Helper()
	rows, err := s.ReadTurnSummaries(context.Background(), sid)
	if err != nil || len(rows) != 1 {
		t.Fatalf("summary=%+v err=%v", rows, err)
	}
	return rows[0]
}

func TestCheckpointBindingPreservesOwnerAndResolvedPayloadOnUpsert(t *testing.T) {
	s, first := boundSummaryFixture(t)
	ctx := context.Background()
	rowID, err := s.AppendTurnSummaryWithID(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	before := readBoundSummary(t, s, first.SessionID)
	changes := []FileChange{{Path: "a.txt", Kind: "modified"}}
	if err := s.ResolveCheckpointChanges(ctx, first.SessionID, 2, rowID, *first.CheckpointBinding, changes); err != nil {
		t.Fatal(err)
	}
	after := readBoundSummary(t, s, first.SessionID)
	if after.RowID != rowID || after.CheckpointBinding == nil || !after.CheckpointBinding.Resolved || !reflect.DeepEqual(after.FileChanges, changes) {
		t.Fatalf("resolved=%+v", after)
	}
	normalized := after
	normalized.FileChanges, normalized.CheckpointBinding = before.FileChanges, before.CheckpointBinding
	if !reflect.DeepEqual(normalized, before) {
		t.Fatal("CAS changed usage, diagnostics, timestamps or physical identity")
	}
	first.Input = 19
	first.FileChanges = []FileChange{{Path: "wrong.txt", Kind: "deleted"}}
	id, err := s.AppendTurnSummaryWithID(ctx, first)
	if err != nil || id != rowID {
		t.Fatalf("same owner upsert: %d %v", id, err)
	}
	after = readBoundSummary(t, s, first.SessionID)
	if after.Input != 19 || !after.CheckpointBinding.Resolved || !reflect.DeepEqual(after.FileChanges, changes) {
		t.Fatalf("upsert reset durable ownership/payload: %+v", after)
	}
	for _, kind := range []string{"empty", "key", "receipt"} {
		t.Run(kind, func(t *testing.T) {
			conflict := first
			if kind == "empty" {
				conflict.CheckpointBinding = nil
			} else {
				copy := *first.CheckpointBinding
				if kind == "key" {
					copy.Key = strings.Repeat("b", 32)
				} else {
					copy.UserMessageID++
				}
				conflict.CheckpointBinding = &copy
			}
			conflict.Input = 999
			if _, err := s.AppendTurnSummaryWithID(ctx, conflict); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("conflict admitted: %v", err)
			}
			if current := readBoundSummary(t, s, first.SessionID); !reflect.DeepEqual(current, after) {
				t.Fatal("refused upsert changed telemetry")
			}
		})
	}
	if err := s.UpdateTurnFileChanges(ctx, first.SessionID, 2, rowID, nil); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("legacy update replaced bound owner: %v", err)
	}
}

func TestCheckpointBindingCannotUpgradeLegacyOrGuessReceipt(t *testing.T) {
	s, first := boundSummaryFixture(t)
	ctx := context.Background()
	legacy := first
	legacy.CheckpointBinding = nil
	if _, err := s.AppendTurnSummaryWithID(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	before := readBoundSummary(t, s, first.SessionID)
	if _, err := s.AppendTurnSummaryWithID(ctx, first); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("legacy row acquired new owner: %v", err)
	}
	if !reflect.DeepEqual(before, readBoundSummary(t, s, first.SessionID)) {
		t.Fatal("legacy row changed")
	}
	other := first
	other.AssistantSeq = 3
	if _, err := s.AppendTurnSummaryWithID(ctx, other); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing assistant admitted: %v", err)
	}
	for _, raw := range []string{"{}", `{"key":"bad"}`, `{"key":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","user_seq":1,"user_message_id":1}`, `{"key":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","user_seq":1,"user_message_id":"1","unknown":true}`} {
		if decodeCheckpointBinding(raw) != nil {
			t.Fatalf("malformed/noncanonical owner admitted: %s", raw)
		}
	}
}

func TestCheckpointBindingRejectsRewoundRowsAndReusedSequences(t *testing.T) {
	s, first := boundSummaryFixture(t)
	ctx := context.Background()
	oldID, err := s.AppendTurnSummaryWithID(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TruncateFromExact(ctx, first.SessionID, MessageReceipt{Seq: 1, ID: first.CheckpointBinding.UserMessageID}); err != nil {
		t.Fatal(err)
	}
	replacement, err := s.AppendMessageWithReceipt(ctx, first.SessionID, Encoded{Role: "user", Content: "replacement"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendMessage(ctx, first.SessionID, Encoded{Role: "assistant", Content: "new response"}); err != nil {
		t.Fatal(err)
	}
	current := first
	newBinding := *first.CheckpointBinding
	newBinding.Key, newBinding.UserMessageID = strings.Repeat("b", 32), replacement.ID
	current.CheckpointBinding, current.Input = &newBinding, 31
	newID, err := s.AppendTurnSummaryWithID(ctx, current)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{oldID, newID} {
		if err := s.ResolveCheckpointChanges(ctx, first.SessionID, 2, id, *first.CheckpointBinding, []FileChange{{Path: "ghost", Kind: "created"}}); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("stale update admitted: %v", err)
		}
	}
	row := readBoundSummary(t, s, first.SessionID)
	if row.Input != 31 || len(row.FileChanges) != 0 || row.CheckpointBinding.Key != newBinding.Key || row.CheckpointBinding.Resolved {
		t.Fatalf("replacement corrupted: %+v", row)
	}
}

func TestCheckpointBindingCASRejectsWrongPhysicalSummaryAndAssistant(t *testing.T) {
	s, first := boundSummaryFixture(t)
	ctx := context.Background()
	id, err := s.AppendTurnSummaryWithID(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveCheckpointChanges(ctx, first.SessionID, 2, id+1, *first.CheckpointBinding, nil); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("wrong physical row: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM messages WHERE session_id=? AND seq=2", first.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveCheckpointChanges(ctx, first.SessionID, 2, id, *first.CheckpointBinding, nil); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing assistant: %v", err)
	}
}
