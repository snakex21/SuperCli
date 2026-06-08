package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/session"
)

// openHistoryStore creates an in-memory session store for tests.
func openHistoryStore(t *testing.T) *session.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := session.OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// seedHistory inserts a known set of messages into the store.
func seedHistory(t *testing.T, s *session.Store) (sessA, sessB session.Session) {
	t.Helper()
	sessA, _ = s.Create("/a", "gpt-4o", "")
	sessB, _ = s.Create("/a", "gpt-4o", "")
	items := []struct {
		sessID string
		role   string
		text   string
		toolID string
	}{
		{sessA.ID, "user", "konspekt spotkania o refaktoryzacji", ""},
		{sessA.ID, "assistant", "Jasne, zacznijmy od celów refaktoryzacji", ""},
		{sessA.ID, "user", "drugi prompt z listą TODO", ""},
		{sessA.ID, "tool", "output: 42", "call-001"},
		{sessB.ID, "user", "spotkanie jutro o 10:00", ""},
		{sessB.ID, "user", "łąka żółwia w Krakowie", ""},
	}
	for _, it := range items {
		enc, err := session.FromMessage(llm.Message{Role: llm.Role(it.role), Content: it.text, ToolCallID: it.toolID})
		if err != nil {
			t.Fatalf("FromMessage: %v", err)
		}
		if err := s.AppendMessage(context.Background(), it.sessID, enc); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}
	return sessA, sessB
}

func TestSearchHistoryTool_BasicMatch(t *testing.T) {
	s := openHistoryStore(t)
	seedHistory(t, s)
	tool := NewSearchHistory(s)
	spec := tool.Spec()
	if spec.Name != "search_history" {
		t.Errorf("name = %q, want search_history", spec.Name)
	}
	res, _ := spec.Fn(context.Background(), json.RawMessage(`{"query": "refaktoryzacji"}`))
	if res.Err != nil {
		t.Fatalf("Err: %v", res.Err)
	}
	if !strings.Contains(res.Text, "<mark>") {
		t.Errorf("text should contain <mark>, got: %s", res.Text)
	}
	if !strings.Contains(res.Text, "sess=") {
		t.Errorf("text should contain sess= marker, got: %s", res.Text)
	}
}

func TestSearchHistoryTool_EmptyQuery(t *testing.T) {
	s := openHistoryStore(t)
	seedHistory(t, s)
	tool := NewSearchHistory(s)
	res, _ := tool.Spec().Fn(context.Background(), json.RawMessage(`{"query": ""}`))
	if res.Err == nil {
		t.Errorf("expected error for empty query, got text: %s", res.Text)
	}
}

func TestSearchHistoryTool_WhitespaceQuery(t *testing.T) {
	s := openHistoryStore(t)
	seedHistory(t, s)
	tool := NewSearchHistory(s)
	res, _ := tool.Spec().Fn(context.Background(), json.RawMessage(`{"query": "   "}`))
	if res.Err == nil {
		t.Errorf("expected error for whitespace query, got text: %s", res.Text)
	}
}

func TestSearchHistoryTool_BadJSON(t *testing.T) {
	s := openHistoryStore(t)
	tool := NewSearchHistory(s)
	res, _ := tool.Spec().Fn(context.Background(), json.RawMessage(`not json`))
	if res.Err == nil {
		t.Errorf("expected error for bad JSON, got text: %s", res.Text)
	}
}

func TestSearchHistoryTool_NoMatches(t *testing.T) {
	s := openHistoryStore(t)
	seedHistory(t, s)
	tool := NewSearchHistory(s)
	res, _ := tool.Spec().Fn(context.Background(), json.RawMessage(`{"query": "xyznonexistent"}`))
	if res.Err != nil {
		t.Fatalf("Err: %v", res.Err)
	}
	if res.Text != "no matches" {
		t.Errorf("text = %q, want 'no matches'", res.Text)
	}
}

func TestSearchHistoryTool_NilStore(t *testing.T) {
	tool := &SearchHistory{Store: nil, DefaultLimit: 20, MaxLimit: 100}
	res, _ := tool.Spec().Fn(context.Background(), json.RawMessage(`{"query": "x"}`))
	// We surface a friendly text message rather than Err so the
	// model can recover without the conversation getting a tool
	// error event. Verify either Err is set OR text indicates
	// the store is unavailable.
	if res.Err == nil && !strings.Contains(res.Text, "not available") {
		t.Errorf("expected Err or 'not available' text, got text=%q err=%v", res.Text, res.Err)
	}
}

func TestSearchHistoryTool_AllFilters(t *testing.T) {
	s := openHistoryStore(t)
	sessA, _ := seedHistory(t, s)
	tool := NewSearchHistory(s)
	res, _ := tool.Spec().Fn(context.Background(), json.RawMessage(`{
		"query": "refaktoryzacji",
		"session_id": "`+sessA.ID+`",
		"role": "assistant",
		"limit": 5
	}`))
	if res.Err != nil {
		t.Fatalf("Err: %v", res.Err)
	}
	// Both sess-A messages contain "refaktoryzacji" — only the
	// assistant one should match with role=assistant.
	if strings.Count(res.Text, "role=assistant") != 1 {
		t.Errorf("expected exactly 1 assistant hit, got: %s", res.Text)
	}
}

func TestSearchHistoryTool_BadSince(t *testing.T) {
	s := openHistoryStore(t)
	tool := NewSearchHistory(s)
	res, _ := tool.Spec().Fn(context.Background(), json.RawMessage(`{"query": "x", "since": "not-a-date"}`))
	if res.Err == nil {
		t.Errorf("expected error for bad since, got text: %s", res.Text)
	}
}

func TestSearchHistoryTool_BadRole(t *testing.T) {
	s := openHistoryStore(t)
	tool := NewSearchHistory(s)
	res, _ := tool.Spec().Fn(context.Background(), json.RawMessage(`{"query": "x", "role": "banana"}`))
	if res.Err == nil {
		t.Errorf("expected error for bad role, got text: %s", res.Text)
	}
}

func TestSearchHistoryTool_LimitClamped(t *testing.T) {
	s := openHistoryStore(t)
	seedHistory(t, s)
	tool := NewSearchHistory(s)
	tool.MaxLimit = 3
	res, _ := tool.Spec().Fn(context.Background(), json.RawMessage(`{"query": "spotkania OR refaktoryzacji OR TODO OR spotkanie OR Krakowie", "limit": 100}`))
	if res.Err != nil {
		t.Fatalf("Err: %v", res.Err)
	}
	// 5 messages total, 4 match (sess-A: 1 user + 1 assistant + 1 user with TODO; sess-B: 1 user with "spotkanie"). Wait
	// count: sess-A user "refaktoryzacji" (matches), sess-A assistant "refaktoryzacji" (matches),
	// sess-A user "TODO" (matches), sess-B user "spotkanie" (matches). 4 matches, limit 100 → clamped to 3.
	hits := strings.Count(res.Text, "[")
	if hits > 3 {
		t.Errorf("hits = %d, want <= 3 (max clamped)", hits)
	}
}

func TestSearchHistoryTool_DefaultLimitApplied(t *testing.T) {
	s := openHistoryStore(t)
	sess, _ := s.Create("/a", "m", "")
	for i := 0; i < 25; i++ {
		enc, _ := session.FromMessage(llm.Message{Role: llm.RoleUser, Content: "wielokrotnie to samo słowo"})
		_ = s.AppendMessage(context.Background(), sess.ID, enc)
	}
	tool := NewSearchHistory(s)
	tool.DefaultLimit = 7
	tool.MaxLimit = 100
	res, _ := tool.Spec().Fn(context.Background(), json.RawMessage(`{"query": "wielokrotnie"}`))
	if res.Err != nil {
		t.Fatalf("Err: %v", res.Err)
	}
	hits := strings.Count(res.Text, "[")
	if hits != 7 {
		t.Errorf("hits = %d, want 7 (default limit)", hits)
	}
}
