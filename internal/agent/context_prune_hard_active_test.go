package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/core"
)

// Reproduce a long worker's single user turn using real bounded read_output
// replies, not private logs or a live provider. In the observed failure these
// replies alone occupied 190-214k tokens; the actual task was under 1k.
func retrievedTaskLoop(t *testing.T, target int) *Loop {
	t.Helper()
	ctx := context.Background()
	l := pruneLoop(t, 272000, 0)
	l.registry = tools.NewRegistry()
	l.registry.EnsureReadOutput()
	page := func(label string) string {
		return label + strings.Repeat("source evidence retained for this task. ", 240)
	}
	first := page("PAGE-ZERO\n")[:8192]
	second := page("PAGE-ONE\n")[:8192]
	preview := l.registry.ModelResultContentContext(ctx, "source", tools.Result{RetainedText: first + second})
	handle := core.StoredOutputHandle(preview)
	if handle == "" {
		t.Fatal("source was not retained")
	}
	l.Messages = []llm.Message{
		{Role: llm.RoleSystem, Content: "Never alter technical placeholders."},
		{Role: llm.RoleUser, Content: "Proofread the assigned locale files. Preserve keys, placeholders, and existing unrelated changes. Do not publish or contact real models."},
	}
	for i := 0; i < 250; i++ {
		appendRetrievedPage(t, l, handle, i, 8192*(1-i%2), 8192)
		if l.EstimateNextRequestTokens() >= target {
			return l
		}
	}
	t.Fatalf("fixture failed to reach target: %d < %d", l.EstimateNextRequestTokens(), target)
	return nil
}

func appendRetrievedPage(t *testing.T, l *Loop, handle string, id, offset, limit int) {
	t.Helper()
	call := llm.ToolCall{ID: fmt.Sprintf("source-%d", id), Name: "read_output", Arguments: fmt.Sprintf(`{"handle":%q,"offset":%d,"limit":%d}`, handle, offset, limit)}
	got := l.invoke(context.Background(), call, make(chan Event, 8))
	if got.failed || len(got.followUps) != 1 {
		t.Fatalf("source read failed: %+v", got)
	}
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}}, got.followUps[0])
	l.invalidateVisibleEstimate()
}

func TestHardWindowPruningReclaimsRetrievedActiveEvidence(t *testing.T) {
	ctx := context.Background()
	l := retrievedTaskLoop(t, 273000)
	before := append([]llm.Message(nil), l.Messages...)
	estimate := l.EstimateNextRequestTokens()
	if _, _, _, err := l.completeOnce(ctx, l.prepareToolRequest(), make(chan Event, 128)); err == nil || !strings.Contains(err.Error(), "active user turn is preserved") {
		t.Fatalf("fixture did not reproduce the pre-send failure: %v", err)
	}
	if gain := l.maybePruneToolResults(ctx, nil); gain <= 0 {
		t.Fatalf("active retrieved evidence stayed pinned at hard capacity: estimate=%d window=%d", estimate, l.window())
	}
	after := l.EstimateNextRequestTokens()
	if after > autoCompactThreshold(l.window()) {
		t.Fatalf("pruning did not restore generation headroom: %d -> %d", estimate, after)
	}
	for i, old := range before {
		current := l.Messages[i]
		if old.Role != llm.RoleTool && !reflect.DeepEqual(old, current) {
			t.Fatalf("task, policy or tool-call arguments changed at %d", i)
		}
		if old.ToolCallID != current.ToolCallID || old.Name != current.Name {
			t.Fatalf("tool protocol changed at %d", i)
		}
	}
	for i := len(before) - 2; i < len(before); i++ {
		if !reflect.DeepEqual(before[i], l.Messages[i]) {
			t.Fatalf("latest evidence changed at %d", i)
		}
	}
	marker := l.Messages[3].Content
	start := strings.Index(marker, "read_output ")
	if !strings.HasPrefix(marker, pruneMarkerPrefix) || start < 0 {
		t.Fatalf("old result has no recovery instruction: %s", marker)
	}
	args := strings.TrimSuffix(marker[start+len("read_output "):], "]")
	recovered := l.invoke(ctx, llm.ToolCall{ID: "recover", Name: "read_output", Arguments: args}, make(chan Event, 8))
	if recovered.failed || len(recovered.followUps) != 1 || !strings.Contains(recovered.followUps[0].Content, "PAGE-ONE") {
		t.Fatalf("the exact old retrieved page was not recoverable: %+v", recovered)
	}
	summaries := 0
	l.summarizer = func(context.Context, llm.Provider, []llm.Message) (string, error) {
		summaries++
		return "not needed", nil
	}
	if err := l.maybeAutoCompact(ctx, nil, ""); err != nil || summaries != 0 {
		t.Fatalf("retrieval pruning should need no summary model call: calls=%d err=%v", summaries, err)
	}
	if _, _, _, err := l.completeOnce(ctx, l.prepareToolRequest(), make(chan Event, 128)); err != nil {
		t.Fatalf("next provider request still failed: %v", err)
	}
	t.Logf("worker-style active turn: %d -> %d tokens, original task and latest evidence intact, archived page recoverable, zero summary calls", estimate, after)
}

func TestHardWindowPruningRechecksRetrievedEvidenceAfterRefusal(t *testing.T) {
	l := retrievedTaskLoop(t, 238000)
	before := l.EstimateNextRequestTokens()
	if before >= autoCompactThreshold(l.window()) {
		t.Fatal("fixture already crossed the hard boundary")
	}
	if gain := l.maybePruneToolResults(context.Background(), nil); gain != 0 {
		t.Fatal("retrieved evidence was discarded before hard pressure")
	}
	if l.pruneRefusedEst != before {
		t.Fatal("fixture did not memoize the refusal")
	}
	handle := core.StoredOutputHandle(l.registry.ModelResultContentContext(context.Background(), "extra", tools.Result{RetainedText: strings.Repeat("recent source. ", 1000)}))
	for i := 0; l.EstimateNextRequestTokens() <= autoCompactThreshold(l.window()); i++ {
		appendRetrievedPage(t, l, handle, 1000+i, 0, 1024)
	}
	estimate := l.EstimateNextRequestTokens()
	if float64(estimate) >= float64(before)*(1+pruneRecheckGrowFrac) {
		t.Fatal("fixture grew enough to bypass the ordinary refusal memo")
	}
	if gain := l.maybePruneToolResults(context.Background(), nil); gain <= 0 {
		t.Fatal("crossing the hard boundary did not invalidate the retrieval-protection refusal")
	}
}

func TestHardWindowPruningLongWorkerAndContinuation(t *testing.T) {
	ctx := context.Background()
	l := retrievedTaskLoop(t, 238000)
	originalTask := l.Messages[1]
	var source struct{ Handle string }
	if err := json.Unmarshal([]byte(l.Messages[2].ToolCalls[0].Arguments), &source); err != nil {
		t.Fatal(err)
	}
	prunes, summaries := 0, 0
	l.summarizer = func(context.Context, llm.Provider, []llm.Message) (string, error) {
		summaries++
		return "must not need a model to discard archived read pages", nil
	}
	for phase := 0; phase < 2; phase++ {
		if phase == 1 {
			l.Messages = append(l.Messages, llm.Message{Role: llm.RoleAssistant, Content: "First batch verified; preserve those edits."}, llm.Message{Role: llm.RoleUser, Content: "Continue the remaining assigned files without changing the original constraints."})
			l.invalidateVisibleEstimate()
		}
		for step := 0; step < 120; step++ {
			appendRetrievedPage(t, l, source.Handle, 2000+phase*120+step, 8192*(step%2), 8192)
			latest := l.Messages[len(l.Messages)-1]
			if l.maybePruneToolResults(ctx, nil) > 0 {
				prunes++
			}
			if err := l.maybeAutoCompact(ctx, nil, ""); err != nil {
				t.Fatal(err)
			}
			if estimate := l.EstimateNextRequestTokens(); estimate > l.window() {
				t.Fatalf("phase %d step %d would overflow: %d/%d", phase, step, estimate, l.window())
			}
			if !reflect.DeepEqual(latest, l.Messages[len(l.Messages)-1]) || !reflect.DeepEqual(originalTask, l.Messages[1]) {
				t.Fatal("the active task or newly received evidence changed")
			}
		}
		if _, _, _, err := l.completeOnce(ctx, l.prepareToolRequest(), make(chan Event, 128)); err != nil {
			t.Fatalf("phase %d could not continue: %v", phase, err)
		}
	}
	if prunes < 2 || summaries != 0 {
		t.Fatalf("expected bounded archive reclamation, got prunes=%d summary calls=%d", prunes, summaries)
	}
	t.Logf("240 additional bounded retrievals across a worker continuation: %d archive batches, zero summary calls, every request fits", prunes)
}

func TestHardWindowPruningStillProtectsOnlyFreshToolBatch(t *testing.T) {
	l := pruneLoop(t, 16000, 0)
	l.Messages = []llm.Message{
		{Role: llm.RoleUser, Content: "Keep the current evidence; do not silently truncate this task."},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "fresh", Name: "read_output", Arguments: `{"handle":"out_0123456789"}`}}},
		{Role: llm.RoleTool, Name: "read_output", ToolCallID: "fresh", Content: strings.Repeat("fresh required evidence ", 6000)},
	}
	before := append([]llm.Message(nil), l.Messages...)
	if l.EstimateNextRequestTokens() <= l.window() {
		t.Fatal("fixture is not under hard pressure")
	}
	if gain := l.maybePruneToolResults(context.Background(), nil); gain != 0 || !reflect.DeepEqual(before, l.Messages) {
		t.Fatal("a single oversized fresh batch must not silently lose evidence")
	}
}
