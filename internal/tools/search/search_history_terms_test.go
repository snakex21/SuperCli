package search

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

func TestSearchHistoryBareFileTermsReachStoredEvidence(t *testing.T) {
	store := openHistoryStore(t)
	sess, err := store.Create("/fixture", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	enc, err := session.FromMessage(llm.Message{Role: llm.RoleTool, ToolCallID: "read", Name: "read_lines", Content: "src/retry.go RetryDelay=235 MaxAttempts=7"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.AppendMessage(context.Background(), sess.ID, enc); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"retry.go", "RetryDelay MaxAttempts retry.go", "src/retry.go AND RetryDelay", "missing.go OR src/retry.go"} {
		raw, _ := json.Marshal(map[string]any{"query": query, "session_id": sess.ID, "role": "tool", "limit": 1})
		result, err := NewSearchHistory(store).Spec().Fn(context.Background(), raw)
		if err != nil || result.Err != nil || !strings.Contains(result.Text, "235") || !strings.Contains(result.Text, "sess="+sess.ID) {
			t.Fatalf("query=%q err=%v/%v result=%s", query, err, result.Err, result.Text)
		}
	}
}

func TestHistoryQueryTermsPreservesFTSOperators(t *testing.T) {
	for _, query := range []string{
		"RetryDelay MaxAttempts", "alpha OR beta AND gamma NOT delta",
		`"quoted phrase" OR prefix*`,
		`content:^alpha + beta`,
		`NEAR(alpha beta, 3)`,
		`-content:alpha`,
		`- {content}:alpha`,
		`{content}:(alpha OR "retry.go")`,
		`"a""b" AND "C:\src\retry.go"`,
		`retry.go "unterminated`,
		"alpha OR", "(alpha", "unknown:alpha", "C++", "zażółć",
	} {
		if got := historyQueryTerms(query); got != query {
			t.Fatalf("%q became %q", query, got)
		}
	}
	for _, pair := range [][2]string{
		{"retry.go", `"retry.go"`},
		{"RetryDelay MaxAttempts retry.go", `RetryDelay MaxAttempts "retry.go"`},
		{`C:\src\retry.go`, `"C:\src\retry.go"`},
		{"C:/src/retry.go", `"C:/src/retry.go"`},
		{`\\server\share\retry.go`, `"\\server\share\retry.go"`},
		{"src/retry.go AND fix-123", `"src/retry.go" AND "fix-123"`},
		{"(retry.go OR other.ts) NOT broken-file", `("retry.go" OR "other.ts") NOT "broken-file"`},
		{"content:retry.go*", `content:"retry.go"*`},
		{"NEAR(retry.go limit, 3)", `NEAR("retry.go" limit, 3)`},
		{`"alpha"retry.go`, `"alpha" "retry.go"`},
		{`retry.go"alpha"`, `"retry.go" "alpha"`},
	} {
		if got := historyQueryTerms(pair[0]); got != pair[1] {
			t.Fatalf("%q => %q want %q", pair[0], got, pair[1])
		}
		if got := historyQueryTerms(pair[1]); got != pair[1] {
			t.Fatalf("not stable: %q", got)
		}
	}
}

func TestSearchHistoryTermsDoNotBroadenBooleanQueries(t *testing.T) {
	store := openHistoryStore(t)
	sess, err := store.Create("/fixture", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"retry.go RetryDelay=235", "other.go MaxAttempts=7", `C:\src\retry.go RetryDelay=9001`} {
		enc, err := session.FromMessage(llm.Message{Role: llm.RoleUser, Content: text})
		if err != nil {
			t.Fatal(err)
		}
		if err = store.AppendMessage(context.Background(), sess.ID, enc); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		query     string
		count     int
		wantError bool
	}{
		{"retry.go AND MaxAttempts", 0, false},
		{"retry.go MaxAttempts", 0, false},
		{"retry.go NOT 9001", 1, false},
		{"retry.go OR other.go", 3, false},
		{`C:\src\retry.go`, 1, false},
		{"NEAR(retry.go RetryDelay, 2)", 2, false},
		{"content:retry.go", 2, false},
		{"{content}:retry.go", 2, false},
		{"retry.go AND", 0, true},
		{"unknown:retry.go", 0, true},
		{`retry.go "unclosed`, 0, true},
	} {
		raw, _ := json.Marshal(map[string]any{"query": tc.query})
		got, err := NewSearchHistory(store).Spec().Fn(context.Background(), raw)
		if err != nil || (got.Err != nil) != tc.wantError {
			t.Fatalf("%q: %v / %v", tc.query, err, got.Err)
		}
		if !tc.wantError && strings.Count(got.Text, "sess=") != tc.count {
			t.Fatalf("%q: %s", tc.query, got.Text)
		}
	}
}
