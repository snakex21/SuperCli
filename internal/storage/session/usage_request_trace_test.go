package session

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
)

func approvedUsageTrace() *llm.RequestTrace {
	last := llm.RequestAttemptTrace{State: "unavailable", Unavailable: "non_replayable"}
	return &llm.RequestTrace{Version: 1, Attempts: 2, First: llm.RequestAttemptTrace{
		Scope: "0123456789abcdef0123456789abcdef", Sequence: 1, State: "first", BlockBytes: 1024, BodyBytes: 123,
	}, Last: &last}
}

func TestUsageRequestTraceUpgradeRoundTripAndDeletion(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sess := mustCreateUsageSession(t, s, "/trace-upgrade")
	if err := s.AppendUsage(ctx, UsageRecord{SessionID: sess.ID, Input: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`ALTER TABLE session_usage DROP COLUMN request_trace_json`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.ReadUsage(ctx, sess.ID)
	if err != nil || len(rows) != 1 || rows[0].RequestTrace != nil {
		t.Fatalf("old trace: %+v, %v", rows, err)
	}
	trace := approvedUsageTrace()
	if err := s.AppendUsage(ctx, UsageRecord{SessionID: sess.ID, Input: 10, RequestTrace: trace}); err != nil {
		t.Fatal(err)
	}
	rows, err = s.ReadUsage(ctx, sess.ID)
	if err != nil || len(rows) != 2 || !reflect.DeepEqual(rows[1].RequestTrace, trace) {
		t.Fatalf("trace roundtrip: %+v, %v", rows, err)
	}
	var encoded string
	if err := s.db.QueryRow(`SELECT request_trace_json FROM session_usage WHERE session_id=? AND call_seq=2`, sess.ID).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	if encoded == "" || len(encoded) > maxRequestTraceJSON {
		t.Fatalf("encoded trace length=%d", len(encoded))
	}
	var traceColumns int
	if err := s.db.QueryRow(`SELECT count(*) FROM pragma_table_info('billing_usage') WHERE name LIKE '%trace%'`).Scan(&traceColumns); err != nil || traceColumns != 0 {
		t.Fatalf("billing trace columns=%d err=%v", traceColumns, err)
	}
	if err := s.Delete(sess.ID); err != nil {
		t.Fatal(err)
	}
	rows, err = s.ReadRecentUsage(ctx, sess.ID, 50)
	if err != nil || len(rows) != 0 {
		t.Fatalf("deleted trace retained: %+v %v", rows, err)
	}
	journal := collectBilling(t, s, sess.ID, time.Time{})
	if len(journal) != 2 {
		t.Fatalf("billing removed: %+v", journal)
	}
	for _, row := range journal {
		if row.RequestTrace != nil {
			t.Fatal("trace copied into billing")
		}
	}
}

func TestUsageRequestTraceRejectsUnsafeSchema(t *testing.T) {
	valid := approvedUsageTrace()
	good := encodeRequestTrace(valid)
	if good == "" || !reflect.DeepEqual(decodeRequestTrace(good), valid) {
		t.Fatal("valid trace rejected")
	}
	for name, data := range map[string]string{
		"empty": "", "null": "null", "unknown": strings.Replace(good, `"version":1`, `"version":1,"prompt":"private"`, 1),
		"nested unknown": strings.Replace(good, `"state":"first"`, `"state":"first","url":"private"`, 1),
		"version":        strings.Replace(good, `"version":1`, `"version":2`, 1),
		"state":          strings.Replace(good, `"state":"first"`, `"state":"private"`, 1),
		"scope":          strings.Replace(good, valid.First.Scope, "private", 1),
		"reason":         strings.Replace(good, "non_replayable", "private", 1),
		"large":          good + strings.Repeat(" ", maxRequestTraceJSON), "trailing": good + `{}`, "broken": `{`,
	} {
		t.Run(name, func(t *testing.T) {
			if got := decodeRequestTrace(data); got != nil {
				t.Fatalf("accepted unsafe trace: %+v", got)
			}
		})
	}
	bad := *valid
	bad.First.State = "private"
	if encodeRequestTrace(&bad) != "" {
		t.Fatal("unsafe trace persisted")
	}
	s := openTestStore(t)
	sess := mustCreateUsageSession(t, s, "/trace-invalid")
	if err := s.AppendUsage(context.Background(), UsageRecord{SessionID: sess.ID, Input: 10, RequestTrace: &bad}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE session_usage SET request_trace_json=? WHERE session_id=?`, strings.Replace(good, `"version":1`, `"version":1,"secret":"private"`, 1), sess.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ReadUsage(context.Background(), sess.ID)
	if err != nil || len(rows) != 1 || rows[0].RequestTrace != nil {
		t.Fatalf("unsafe persisted trace decoded: %+v %v", rows, err)
	}
	var closed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(good), &closed); err != nil || len(closed) != 4 {
		t.Fatalf("trace schema: %s %v", good, err)
	}
}

func TestReadRecentUsageBoundsOrderAndIsolation(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	sess := mustCreateUsageSession(t, s, "/recent")
	other := mustCreateUsageSession(t, s, "/recent")
	for seq := 1; seq <= 205; seq++ {
		if err := s.AppendUsage(ctx, UsageRecord{SessionID: sess.ID, CallSeq: seq, Input: int64(seq)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []UsageRecord{{SessionID: sess.ID, CallSeq: 300, Source: "legacy", Input: 900}, {SessionID: other.ID, Input: 888}} {
		if err := s.AppendUsage(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ limit, want int }{{0, 50}, {-1, 50}, {1, 1}, {50, 50}, {200, 200}, {201, 200}, {10000, 200}} {
		rows, err := s.ReadRecentUsage(ctx, sess.ID, tc.limit)
		if err != nil || len(rows) != tc.want {
			t.Fatalf("limit %d: len=%d err=%v", tc.limit, len(rows), err)
		}
		for i, row := range rows {
			if row.CallSeq != 205-i || row.SessionID != sess.ID || row.Source == "legacy" {
				t.Fatalf("limit %d row %d: %+v", tc.limit, i, row)
			}
		}
	}
	rows, err := s.ReadRecentUsage(ctx, "missing", 50)
	if err != nil || len(rows) != 0 {
		t.Fatalf("missing session: %+v %v", rows, err)
	}
}
