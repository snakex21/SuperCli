package session

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func seedRecentTurnProjection(b testing.TB, count, changes int, binding bool) *Store {
	b.Helper()
	s, err := OpenStore(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	sess, err := s.Create(b.TempDir(), "synthetic", "telemetry projection")
	if err != nil {
		b.Fatal(err)
	}
	files := make([]FileChange, changes)
	for i := range files {
		files[i] = FileChange{Path: fmt.Sprintf("synthetic/project/path/component-%04d/data.go", i), Kind: "modified"}
	}
	fileJSON, err := json.Marshal(files)
	if err != nil {
		b.Fatal(err)
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		b.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO session_turns (session_id, assistant_seq, duration_ms, input_tokens, output_tokens,
		tool_calls, tool_failures, steps, model_calls, helper_calls, aux_calls, aux_us, phases_json, file_changes_json, tool_diag_json, created_at, checkpoint_binding)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		b.Fatal(err)
	}
	defer stmt.Close()
	now := time.Now().UTC()
	for i := 0; i < count; i++ {
		owner := ""
		if binding {
			owner, err = (CheckpointBinding{Key: fmt.Sprintf("%032x", i+1), UserSeq: i*2 + 1, UserMessageID: int64(i + 1)}).encoded(true)
			if err != nil {
				b.Fatal(err)
			}
		}
		if _, err := stmt.Exec(sess.ID, i*2+2, 900, 1200+i, 100+i, 2, i%3, 2+i%4, 2, 1, 1, 30000,
			`{"context_prepare":1100,"backend_wait":750000,"tool_execution":120000,"tool:read_many":120000}`, string(fileJSON),
			`{"failures":{"read_many":1},"messages":{"read_many":"synthetic failure"},"noop_searches":1}`, now.Add(time.Duration(i)*time.Nanosecond).UnixNano(), owner); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	return s
}

func TestRecentTurnTelemetryPreservesConsumedFields(t *testing.T) {
	s := seedRecentTurnProjection(t, 8, 64, true)
	ctx, since := context.Background(), time.Now().Add(-time.Hour)
	full, err := s.ReadRecentTurnSummaries(ctx, since, 5)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := s.ReadRecentTurnTelemetry(ctx, since, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(full) != 5 || len(projected) != 5 {
		t.Fatal("window/limit changed")
	}
	for i := range full {
		if full[i].CheckpointBinding == nil || len(full[i].FileChanges) != 64 {
			t.Fatal("fixture does not contain unused payloads")
		}
		full[i].RowID, full[i].CheckpointBinding, full[i].FileChanges = 0, nil, nil
		if !reflect.DeepEqual(full[i], projected[i]) {
			t.Fatal("projection lost telemetry, diagnostics, phases, order or timestamps")
		}
	}
}

func BenchmarkRecentTurnTelemetryProjection(b *testing.B) {
	for _, fixture := range []struct {
		name    string
		changes int
		binding bool
	}{{"ordinary", 0, false}, {"file-changes", 64, false}, {"deferred", 64, true}} {
		b.Run(fixture.name, func(b *testing.B) {
			s := seedRecentTurnProjection(b, 2000, fixture.changes, fixture.binding)
			ctx, since := context.Background(), time.Now().Add(-time.Hour)
			for _, projected := range []bool{false, true} {
				name := "full"
				if projected {
					name = "projected"
				}
				b.Run(name, func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						var rows []TurnSummary
						var err error
						if projected {
							rows, err = s.ReadRecentTurnTelemetry(ctx, since, 2000)
						} else {
							rows, err = s.ReadRecentTurnSummaries(ctx, since, 2000)
						}
						if err != nil || len(rows) != 2000 {
							b.Fatalf("rows=%d err=%v", len(rows), err)
						}
					}
				})
			}
		})
	}
}

func TestRecentTurnTelemetryBoundsAndCancellation(t *testing.T) {
	s := seedRecentTurnProjection(t, 2001, 0, false)
	since := time.Now().Add(-time.Hour)
	for _, limit := range []int{0, -1, 2001, 1} {
		rows, err := s.ReadRecentTurnTelemetry(context.Background(), since, limit)
		want := 2000
		if limit == 1 {
			want = 1
		}
		if err != nil || len(rows) != want {
			t.Fatalf("limit=%d rows=%d err=%v", limit, len(rows), err)
		}
	}
	rows, err := s.ReadRecentTurnTelemetry(context.Background(), time.Now().Add(time.Hour), 2000)
	if err != nil || len(rows) != 0 {
		t.Fatalf("future window rows=%d err=%v", len(rows), err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.ReadRecentTurnTelemetry(ctx, since, 2000); err == nil {
		t.Fatal("canceled read succeeded")
	}
}
