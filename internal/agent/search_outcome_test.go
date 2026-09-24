package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestSearchMissDoesNotInventFailureOrBlockCompletion(t *testing.T) {
	for _, result := range []tools.Result{
		{Text: "No results found"},
		{Text: "not found"},
		{Text: "no matches"},
		{Text: "No results found", Err: errors.New("backend unavailable")},
	} {
		t.Run(result.Text+":"+fmtSearchError(result.Err), func(t *testing.T) {
			reg := tools.NewRegistry()
			reg.MustRegister(tools.Tool{Name: "search_records", Description: "search records", ReadOnly: true, Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return result, nil }})
			completions := 0
			reg.MustRegister(tools.Tool{Name: "goal", Description: "manage task", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				completions++
				return tools.Result{Text: "completed"}, nil
			}})
			loop := &Loop{registry: reg}
			out := make(chan Event, 16)
			found := loop.invoke(context.Background(), llm.ToolCall{ID: "search", Name: "search_records", Arguments: "{}"}, out)
			wantFailure := result.Err != nil
			if found.failed != wantFailure || loop.concreteFailure.Load() != wantFailure {
				t.Fatalf("search failed=%v concreteFailure=%v result=%+v", found.failed, loop.concreteFailure.Load(), result)
			}
			if !wantFailure && (len(found.followUps) != 1 || found.followUps[0].Content != result.Text) {
				t.Fatalf("changed evidence: %+v", found.followUps)
			}
			completed := loop.invoke(context.Background(), llm.ToolCall{ID: "goal", Name: "goal", Arguments: "{\"action\":\"complete_task\",\"task_seq\":1}"}, out)
			if completed.failed != wantFailure || (completions == 1) == wantFailure {
				t.Fatalf("completion failed=%v calls=%d", completed.failed, completions)
			}
		})
	}
}
func fmtSearchError(err error) string {
	if err != nil {
		return err.Error()
	}
	return "success"
}

func TestSearchHitFilenameCannotBecomeVerificationFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "not found.go"), []byte("needle\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.NewSearchCode(root).Spec())
	loop := &Loop{registry: reg, baseDir: root}
	out := make(chan Event, 8)
	got := loop.invoke(context.Background(), llm.ToolCall{ID: "hit", Name: "search_code", Arguments: "{\"query\":\"needle\",\"context\":0}"}, out)
	if got.failed || loop.concreteFailure.Load() || len(got.followUps) != 1 || !strings.Contains(got.followUps[0].Content, "not found.go:1:needle") {
		t.Fatalf("real hit misclassified: %+v", got)
	}
}
