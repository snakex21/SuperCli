package webgui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

func recentTestTrace() *llm.RequestTrace {
	return &llm.RequestTrace{Version: 1, Attempts: 1, First: llm.RequestAttemptTrace{
		Scope: "0123456789abcdef0123456789abcdef", Sequence: 1, State: "first", BlockBytes: 1024, BodyBytes: 123,
	}}
}

func TestUsageRecentAPIClosedProjectionLimitsAndCompatibility(t *testing.T) {
	s := newTestServer(t, false)
	store, err := s.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(s.eng.Home(), "private-model", "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.Create(s.eng.Home(), "other-private-model", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	at := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	for seq := 1; seq <= 205; seq++ {
		u := session.UsageRecord{SessionID: sess.ID, CallSeq: seq, Provider: "private-provider", ProviderType: "private-type",
			EndpointHost: "private-endpoint.invalid", Model: "private-model", Input: 10, Output: 3, Source: "model", CreatedAt: at,
			DurationMS: 200, TTFTMS: 50, HasTiming: true, HasCachedInput: true, PrefillBudgetSource: "private-config"}
		if seq == 205 {
			u.RequestTrace = recentTestTrace()
		}
		if seq == 204 {
			u.Source = "private-source"
		}
		if err := store.AppendUsage(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	for _, u := range []session.UsageRecord{{SessionID: sess.ID, CallSeq: 300, Source: "legacy", Input: 90}, {SessionID: other.ID, Input: 888, RequestTrace: recentTestTrace()}} {
		if err := store.AppendUsage(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	request := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host, req.RemoteAddr = "127.0.0.1", "127.0.0.1:1234"
		s.Handler().ServeHTTP(rec, req)
		return rec
	}
	base := "/api/usage?session=" + sess.ID
	for _, tc := range []struct {
		query string
		want  int
	}{{"", 50}, {"&limit=1", 1}, {"&limit=0", 50}, {"&limit=-2", 50}, {"&limit=200", 200}, {"&limit=999", 200}} {
		rec := request(base + "&view=recent" + tc.query)
		var got recentUsageView
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" || got.SessionID != sess.ID || len(got.Rows) != tc.want {
			t.Fatalf("query %q status=%d result=%+v", tc.query, rec.Code, got)
		}
		for i, row := range got.Rows {
			if row.Seq != 205-i {
				t.Fatalf("query %q row %d seq=%d", tc.query, i, row.Seq)
			}
		}
		first := got.Rows[0]
		if !reflect.DeepEqual(first.RequestTrace, recentTestTrace()) || !first.CreatedAt.Equal(at) || first.Purpose != "main" || first.Tokens.In != 10 || first.Tokens.Out != 3 || first.Tokens.Cache != 0 || !first.Tokens.CacheReported || first.DurationMS != 200 || first.TTFTMS != 50 || !first.HasTiming {
			t.Fatalf("projection=%+v", first)
		}
		if len(got.Rows) > 1 && (got.Rows[1].RequestTrace != nil || got.Rows[1].Purpose != "other") {
			t.Fatalf("unknown fields fabricated/leaked: %+v", got.Rows[1])
		}
		if strings.Contains(rec.Body.String(), "private") || strings.Contains(rec.Body.String(), other.ID) {
			t.Fatalf("private content in DTO: %s", rec.Body.String())
		}
		var closed struct {
			Rows []map[string]json.RawMessage `json:"rows"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &closed); err != nil {
			t.Fatal(err)
		}
		allowed := map[string]bool{"seq": true, "created_at": true, "purpose": true, "tokens": true, "duration_ms": true, "ttft_ms": true, "has_timing": true, "request_trace": true}
		for _, row := range closed.Rows {
			if len(row) != len(allowed) {
				t.Fatalf("unexpected keys: %v", row)
			}
			for key := range row {
				if !allowed[key] {
					t.Fatalf("unapproved key %q", key)
				}
			}
			var tokens map[string]json.RawMessage
			if err := json.Unmarshal(row["tokens"], &tokens); err != nil {
				t.Fatal(err)
			}
			if len(tokens) != 4 || tokens["in"] == nil || tokens["out"] == nil || tokens["cache"] == nil || tokens["cache_reported"] == nil {
				t.Fatalf("token keys: %v", tokens)
			}
		}
	}
	// The old aggregate path remains the default, including legacy totals.
	for _, suffix := range []string{"", "&limit=bad", "&view=unknown"} {
		rec := request(base + suffix)
		var got usageView
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if rec.Code != 200 || got.Scope != "session" || got.Totals.Calls != 205 || got.Totals.Input != 2140 || got.Totals.LegacyRecords != 1 || strings.Contains(rec.Body.String(), "request_trace") {
			t.Fatalf("aggregate changed: status=%d %+v", rec.Code, got)
		}
	}
	isolated := request("/api/usage?session=" + other.ID + "&view=recent")
	var got recentUsageView
	if err := json.Unmarshal(isolated.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if isolated.Code != 200 || len(got.Rows) != 1 || got.Rows[0].Tokens.In != 888 {
		t.Fatalf("session isolation: %+v", got)
	}
}

func TestUsageRecentAPIRequiresSessionAndWorkspace(t *testing.T) {
	s := newTestServer(t, false)
	store, err := s.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.Create(s.eng.Home()+"/other-workspace", "m", "")
	if err != nil {
		t.Fatal(err)
	}
	empty, err := store.Create(s.eng.Home(), "m", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, query string
		want          int
	}{
		{http.MethodGet, "view=recent", 400},
		{http.MethodGet, "view=recent&session=missing", 400},
		{http.MethodGet, "view=recent&session=" + other.ID, 400},
		{http.MethodGet, "view=recent&session=" + empty.ID + "&limit=bad", 400},
		{http.MethodPost, "view=recent&session=" + empty.ID, 405},
		{http.MethodGet, "view=recent&session=" + empty.ID, 200},
	} {
		rec := httptest.NewRecorder()
		s.handleUsage(rec, httptest.NewRequest(tc.method, "/api/usage?"+tc.query, nil))
		if rec.Code != tc.want {
			t.Fatalf("%s %s status=%d body=%s", tc.method, tc.query, rec.Code, rec.Body.String())
		}
		if rec.Code == 200 && !strings.Contains(rec.Body.String(), `"rows":[]`) {
			t.Fatalf("empty rows must be array: %s", rec.Body.String())
		}
	}
}

func TestGUIUsageSinkPropagatesTraceAndKeepsZeroTokenFilter(t *testing.T) {
	s := newTestServer(t, false)
	store, err := s.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(s.eng.Home(), "fixture-model", "")
	if err != nil {
		t.Fatal(err)
	}
	sink := s.eng.usageCallSink(store, sess.ID)
	trace := recentTestTrace()
	sink(llm.CallStat{Purpose: llm.PurposeMain, RequestTrace: trace, Failed: true})
	sink(llm.CallStat{Purpose: llm.PurposeCompact, TokensIn: 10, TokensOut: 3, HasCachedInput: true, RequestTrace: trace})
	sink(llm.CallStat{Purpose: llm.PurposeTask, TokensIn: 10, TokensCached: 2})
	bad := *trace
	bad.First.State = "private"
	sink(llm.CallStat{Purpose: llm.PurposeMain, TokensIn: 10, RequestTrace: &bad})
	rows, err := store.ReadUsage(context.Background(), sess.ID)
	if err != nil || len(rows) != 3 {
		t.Fatalf("zero-token filter: %+v %v", rows, err)
	}
	if !reflect.DeepEqual(rows[0].RequestTrace, trace) || !rows[0].HasCachedInput || rows[0].CachedInput != 0 || rows[0].Source != llm.PurposeCompact {
		t.Fatalf("trace/cache projection: %+v", rows[0])
	}
	if !rows[1].HasCachedInput || rows[1].RequestTrace != nil || rows[2].HasCachedInput || rows[2].RequestTrace != nil {
		t.Fatalf("unknown trace/cache changed: %+v", rows)
	}
}
