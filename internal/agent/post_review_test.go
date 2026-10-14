package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// postReviewLoop builds a loop whose first model turn calls one tool
// (mutating or read-only) and whose later turns reply with plain text.
func postReviewLoop(t *testing.T, readOnly bool, review *PostReviewConfig) (*Loop, *stubProvider, string) {
	t.Helper()
	root := t.TempDir()
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name: "fixture_edit", Description: "edit", Schema: "{}", ReadOnly: readOnly,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			if !readOnly {
				if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\nfunc A() {}\n"), 0o644); err != nil {
					return tools.Result{}, err
				}
			}
			return tools.Result{Text: "done"}, nil
		},
	})
	reg.MarkAlwaysOn("fixture_edit")
	main := &stubProvider{name: "main", scripts: [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "c1", Name: "fixture_edit", Arguments: "{}"}, FinishReason: "tool_calls"}},
		{{Content: "Gotowe, plik zmieniony."}, {FinishReason: "stop", Usage: &llm.Usage{Input: 10, Output: 2, Total: 12}}},
	}}
	l, err := NewLoop(LoopConfig{Provider: main, Registry: reg, BaseDir: root, MaxSteps: 10, Review: review})
	if err != nil {
		t.Fatal(err)
	}
	return l, main, root
}

func runPostReviewLoop(t *testing.T, l *Loop) (DoneEvent, []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ch, err := l.Run(ctx, "dodaj funkcję A")
	if err != nil {
		t.Fatal(err)
	}
	var done *DoneEvent
	var notices []string
	for ev := range ch {
		switch e := ev.(type) {
		case DoneEvent:
			d := e
			done = &d
		case ErrorEvent:
			t.Fatalf("run failed: %v", e.Err)
		case NoticeEvent:
			notices = append(notices, e.Text)
		}
	}
	if done == nil {
		t.Fatal("no DoneEvent")
	}
	return *done, notices
}

func reviewNotices(notices []string) []string {
	var out []string
	for _, n := range notices {
		if strings.HasPrefix(n, "review (") {
			out = append(out, n)
		}
	}
	return out
}

func countReviewMessages(l *Loop) int {
	n := 0
	for _, m := range l.Messages {
		if strings.Contains(m.Content, "[post-work review]") {
			n++
		}
	}
	return n
}

func TestPostReview_OffMakesNoExtraCall(t *testing.T) {
	l, main, _ := postReviewLoop(t, false, nil)
	_, notices := runPostReviewLoop(t, l)
	if got := atomic.LoadInt32(&main.calls); got != 2 {
		t.Fatalf("main calls = %d, want 2", got)
	}
	if rn := reviewNotices(notices); len(rn) != 0 {
		t.Fatalf("review off emitted notices: %v", rn)
	}
	if countReviewMessages(l) != 0 {
		t.Fatal("review off appended notes")
	}
}

func TestPostReview_ReadOnlyRunSkipsReview(t *testing.T) {
	rev := &scriptedVerdictProvider{name: "reviewer", reply: `{"decision":"accept"}`}
	l, main, _ := postReviewLoop(t, true, &PostReviewConfig{Provider: rev})
	_, notices := runPostReviewLoop(t, l)
	if rev.calls.Load() != 0 {
		t.Fatalf("read-only run called reviewer %d times", rev.calls.Load())
	}
	if l.postReview.snapshot != nil {
		t.Fatal("read-only run took a tree snapshot")
	}
	if got := atomic.LoadInt32(&main.calls); got != 2 {
		t.Fatalf("main calls = %d, want 2", got)
	}
	if rn := reviewNotices(notices); len(rn) != 0 {
		t.Fatalf("unexpected review notices: %v", rn)
	}
}

func TestPostReview_AcceptFinishesWithNotice(t *testing.T) {
	rev := &scriptedVerdictProvider{name: "reviewer", reply: `{"decision":"accept"}`}
	l, main, _ := postReviewLoop(t, false, &PostReviewConfig{Provider: rev})
	done, notices := runPostReviewLoop(t, l)
	if rev.calls.Load() != 1 {
		t.Fatalf("reviewer calls = %d, want 1", rev.calls.Load())
	}
	if got := atomic.LoadInt32(&main.calls); got != 2 {
		t.Fatalf("main calls = %d, want 2", got)
	}
	rn := reviewNotices(notices)
	if len(rn) != 1 || !strings.Contains(rn[0], "accepted") {
		t.Fatalf("review notices = %v", rn)
	}
	// Review usage (40 in / 6 out from the scripted verdict) is counted.
	if done.Usage.Input < 40 || done.Usage.Output < 6 {
		t.Fatalf("review usage not added: %+v", done.Usage)
	}
	if countReviewMessages(l) != 0 {
		t.Fatal("accept must not append notes")
	}
}

func TestPostReview_ReviseSendsNotesBackAndCaps(t *testing.T) {
	rev := &scriptedVerdictProvider{name: "reviewer", reply: `{"decision":"revise","instruction":"add a test for A in a_test.go"}`}
	l, main, _ := postReviewLoop(t, false, &PostReviewConfig{Provider: rev})
	_, notices := runPostReviewLoop(t, l)
	// First answer → revise → main model works again → its second answer is
	// reviewed once more, but the round cap (0 = 1) finishes the Run.
	if got := atomic.LoadInt32(&main.calls); got != 3 {
		t.Fatalf("main calls = %d, want 3", got)
	}
	if rev.calls.Load() != 2 {
		t.Fatalf("reviewer calls = %d, want 2", rev.calls.Load())
	}
	if n := countReviewMessages(l); n != 1 {
		t.Fatalf("review notes appended %d times, want 1", n)
	}
	found := false
	for _, m := range l.Messages {
		if strings.Contains(m.Content, "[post-work review]") && strings.Contains(m.Content, "add a test for A") {
			found = true
		}
	}
	if !found {
		t.Fatal("reviewer instruction missing from appended notes")
	}
	// The main model actually saw the notes on its next request.
	last := main.reqs[len(main.reqs)-1]
	seen := false
	for _, m := range last {
		if strings.Contains(m.Content, "add a test for A") {
			seen = true
		}
	}
	if !seen {
		t.Fatal("main model's next request lacks the reviewer notes")
	}
	rn := reviewNotices(notices)
	if len(rn) != 2 || !strings.Contains(rn[0], "notes sent back") || !strings.Contains(rn[1], "round limit") {
		t.Fatalf("review notices = %v", rn)
	}
}

func TestPostReview_UnparsedVerdictFinishes(t *testing.T) {
	rev := &scriptedVerdictProvider{name: "reviewer", reply: "asdf qwerty 12345"}
	l, main, _ := postReviewLoop(t, false, &PostReviewConfig{Provider: rev, MaxRounds: 3})
	_, notices := runPostReviewLoop(t, l)
	if rev.calls.Load() != 1 {
		t.Fatalf("reviewer calls = %d, want 1", rev.calls.Load())
	}
	if got := atomic.LoadInt32(&main.calls); got != 2 {
		t.Fatalf("main calls = %d, want 2", got)
	}
	if countReviewMessages(l) != 0 {
		t.Fatal("unparsed verdict must not loop back")
	}
	rn := reviewNotices(notices)
	if len(rn) != 1 || !strings.Contains(rn[0], "no readable verdict") {
		t.Fatalf("review notices = %v", rn)
	}
}

func TestPostReview_StateResetsPerRun(t *testing.T) {
	rev := &scriptedVerdictProvider{name: "reviewer", reply: `{"decision":"accept"}`}
	l, _, _ := postReviewLoop(t, false, &PostReviewConfig{Provider: rev})
	runPostReviewLoop(t, l)
	l.resetPostReview("next")
	if l.postReview.mutated || l.postReview.snapshot != nil || l.postReview.rounds != 0 || l.postReview.task != "next" {
		t.Fatalf("state not reset: %+v", &l.postReview)
	}
}
