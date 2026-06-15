package goal

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestHeuristicFromTitle_Empty(t *testing.T) {
	if got := HeuristicFromTitle(""); got != nil {
		t.Errorf("empty title should return nil, got %v", got)
	}
	if got := HeuristicFromTitle("   "); got != nil {
		t.Errorf("whitespace title should return nil, got %v", got)
	}
}

func TestHeuristicFromTitle_Default(t *testing.T) {
	got := HeuristicFromTitle("Ship F8 in production")
	if len(got) < 3 || len(got) > MaxDecomposeTasks {
		t.Errorf("expected 3-8 tasks, got %d: %v", len(got), got)
	}
}

func TestHeuristicFromTitle_BugFix(t *testing.T) {
	got := HeuristicFromTitle("Fix race in cache invalidation")
	if len(got) < 3 {
		t.Errorf("bug-fix path returned %d tasks: %v", len(got), got)
	}
	joined := strings.ToLower(strings.Join(got, " "))
	if !strings.Contains(joined, "reproduc") {
		t.Errorf("bug-fix tasks should include reproduce: %v", got)
	}
}

func TestHeuristicFromTitle_Refactor(t *testing.T) {
	got := HeuristicFromTitle("Refactor storage layer")
	if len(got) < 3 {
		t.Errorf("refactor path returned %d tasks: %v", len(got), got)
	}
}

func TestHeuristicFromTitle_Colons(t *testing.T) {
	got := HeuristicFromTitle("Ship F8: design, implement, test, release")
	if len(got) < 2 {
		t.Errorf("colon split returned %d tasks: %v", len(got), got)
	}
	if !strings.Contains(strings.ToLower(strings.Join(got, " ")), "design") {
		t.Errorf("expected 'design' in tasks: %v", got)
	}
}

func TestHeuristicFromTitle_DashSeparator(t *testing.T) {
	got := HeuristicFromTitle("Build dashboard - fetch metrics - render ui - ship")
	if len(got) < 2 {
		t.Errorf("dash split returned %d tasks: %v", len(got), got)
	}
}

func TestHeuristicFromTitle_LongTruncated(t *testing.T) {
	long := strings.Repeat("a", 500)
	got := HeuristicFromTitle("x: " + long)
	for _, task := range got {
		if len(task) > 120 {
			t.Errorf("task length %d > 120", len(task))
		}
	}
}

// stubProvider is a minimal Provider for ModelDecompose.
type stubProvider struct {
	resp string
	err  error
	calls int
}

func (s *stubProvider) Complete(_ context.Context, _ []Message) (string, error) {
	s.calls++
	if s.err != nil {
		return "", s.err
	}
	return s.resp, nil
}

func TestModelDecompose_EmptyTitle(t *testing.T) {
	p := &stubProvider{}
	_, err := ModelDecompose(context.Background(), p, "m", "", "")
	if !errors.Is(err, ErrEmptyTitle) {
		t.Errorf("expected ErrEmptyTitle, got %v", err)
	}
	if p.calls != 0 {
		t.Errorf("provider should not be called for empty title")
	}
}

func TestModelDecompose_NilProvider(t *testing.T) {
	_, err := ModelDecompose(context.Background(), nil, "m", "x", "")
	if err == nil {
		t.Error("expected error for nil provider")
	}
}

func TestModelDecompose_ParseJSONObject(t *testing.T) {
	p := &stubProvider{
		resp: `{"action":"decompose","tasks":["a","b","c"]}`,
	}
	got, err := ModelDecompose(context.Background(), p, "m", "ship F8", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "a" {
		t.Errorf("got %v", got)
	}
}

func TestModelDecompose_ParseBareArray(t *testing.T) {
	p := &stubProvider{resp: `["a","b","c"]`}
	got, err := ModelDecompose(context.Background(), p, "m", "x", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("got %v", got)
	}
}

func TestModelDecompose_StripFences(t *testing.T) {
	p := &stubProvider{resp: "```json\n{\"action\":\"decompose\",\"tasks\":[\"a\",\"b\"]}\n```"}
	got, err := ModelDecompose(context.Background(), p, "m", "x", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "a" {
		t.Errorf("got %v", got)
	}
}

func TestModelDecompose_ProseAroundJSON(t *testing.T) {
	p := &stubProvider{resp: "Here you go: {\"action\":\"decompose\",\"tasks\":[\"a\",\"b\",\"c\"]} enjoy!"}
	got, err := ModelDecompose(context.Background(), p, "m", "x", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("got %v", got)
	}
}

func TestModelDecompose_LimitMax(t *testing.T) {
	tasks := make([]string, 20)
	for i := range tasks {
		tasks[i] = "task"
	}
	p := &stubProvider{resp: mustJSON(map[string]any{"action": "decompose", "tasks": tasks})}
	got, err := ModelDecompose(context.Background(), p, "m", "x", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > MaxDecomposeTasks {
		t.Errorf("expected <= %d tasks, got %d", MaxDecomposeTasks, len(got))
	}
}

func TestModelDecompose_ProviderError(t *testing.T) {
	p := &stubProvider{err: errors.New("network down")}
	_, err := ModelDecompose(context.Background(), p, "m", "x", "")
	if err == nil || !strings.Contains(err.Error(), "network down") {
		t.Errorf("expected wrapped provider error, got %v", err)
	}
}

func TestModelDecompose_Malformed(t *testing.T) {
	p := &stubProvider{resp: "this is not json at all"}
	_, err := ModelDecompose(context.Background(), p, "m", "x", "")
	if err == nil {
		t.Error("expected error for malformed response")
	}
}

func TestModelDecompose_EmptyResponse(t *testing.T) {
	p := &stubProvider{resp: ""}
	_, err := ModelDecompose(context.Background(), p, "m", "x", "")
	if err == nil {
		t.Error("expected error for empty response")
	}
}

func TestParseDecomposeResponse_TasksOnlyRegex(t *testing.T) {
	// When JSON parsing fails, the regex path should still extract tasks.
	raw := `prefix {"action":"decompose","tasks":["x","y","z"]} suffix`
	got, err := parseDecomposeResponse(raw, MaxDecomposeTasks)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "x" {
		t.Errorf("regex fallback got %v", got)
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
