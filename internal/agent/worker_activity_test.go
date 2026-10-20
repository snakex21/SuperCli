package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestWorkerActivityToolEventsAndReset(t *testing.T) {
	shortArgs := `{"query":"short"}`
	// The byte limit lands inside a multibyte rune.
	longArgs := `{"query":"` + strings.Repeat("界", 1000) + `"}`
	provider := &stubProvider{name: "activity", scripts: [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "short-call", Name: "search_code", Arguments: shortArgs}}, {FinishReason: "tool_calls"}},
		{{ToolCall: &llm.ToolCall{ID: "long-call", Name: "search_code", Arguments: longArgs}}, {FinishReason: "tool_calls"}},
		{{Role: llm.RoleAssistant, Content: "unchanged report"}, {FinishReason: "stop"}},
		{{Role: llm.RoleAssistant, Content: "next report"}, {FinishReason: "stop"}},
	}}
	registry := tools.NewRegistry()
	registry.MustRegister(tools.Tool{
		Name: "search_code", Description: "search", Schema: `{"type":"object"}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: strings.Repeat("PRIVATE_OUTPUT", 1000)}, nil
		},
	})
	w := &Worker{ID: "worker-activity", Agent: "explore", Loop: makeLoop(t, provider, registry, "")}
	before := time.Now().UnixMilli()
	var observed []WorkerActivity
	var kinds []string
	w.progress = func(ev WorkerProgressEvent) {
		kinds = append(kinds, ev.Kind)
		s := w.Snapshot()
		if ev.Kind == "started" && s.Activity != (WorkerActivity{}) {
			t.Errorf("run %d started with stale activity: %+v", s.Runs, s.Activity)
		}
		if ev.Kind != "tool_call" && ev.Kind != "tool_result" {
			return
		}
		a := s.Activity
		if a.Kind != ev.Kind || a.CallID != ev.CallID || a.Tool != ev.Tool {
			t.Errorf("snapshot not updated before progress: %+v / %+v", a, ev)
		}
		if a.At < before || a.At > time.Now().UnixMilli() {
			t.Errorf("activity observation time = %d", a.At)
		}
		if len(a.Args) > 1024 || !utf8.ValidString(a.Args) {
			t.Errorf("invalid bounded args: bytes=%d valid=%v", len(a.Args), utf8.ValidString(a.Args))
		}
		observed = append(observed, a)
	}
	text, err := runWorkerLoop(context.Background(), w, "inspect")
	if err != nil || text != "unchanged report" {
		t.Fatalf("first run = %q, %v", text, err)
	}
	if len(observed) != 4 {
		t.Fatalf("tool activities = %+v", observed)
	}
	if observed[0].Args != shortArgs || observed[1].Args != shortArgs {
		t.Fatalf("short call/result args not preserved: %+v", observed[:2])
	}
	if observed[2].Args == "" || observed[2].Args != observed[3].Args || !strings.HasPrefix(longArgs, observed[2].Args) {
		t.Fatal("long result did not retain bounded matching call args")
	}
	first := w.Snapshot()
	if first.Runs != 1 || first.Activity != observed[3] {
		t.Fatalf("latest snapshot = %+v", first)
	}
	copy := first
	copy.Activity.Args = "changed by reader"
	copy.Activity.Tool = "changed by reader"
	if w.Snapshot().Activity != first.Activity {
		t.Fatal("snapshot activity aliases worker state")
	}
	text, err = runWorkerLoop(context.Background(), w, "continue without tools")
	if err != nil || text != "next report" {
		t.Fatalf("second run = %q, %v", text, err)
	}
	if s := w.Snapshot(); s.Runs != 2 || s.Activity != (WorkerActivity{}) {
		t.Fatalf("next run retained stale activity: %+v", s)
	}
	if first.Activity != observed[3] {
		t.Fatal("retained snapshot changed across runs")
	}
	if got := strings.Join(kinds, ","); got != "started,tool_call,tool_result,tool_call,tool_result,finished,started,finished" {
		t.Fatalf("progress event sequence changed: %s", got)
	}
	encoded, err := json.Marshal(first.Activity)
	if err != nil || strings.Contains(string(encoded), "PRIVATE_OUTPUT") || strings.Contains(string(encoded), "unchanged report") {
		t.Fatalf("activity leaked report/output: %s, %v", encoded, err)
	}
}

func TestWorkerActivityInvalidUTF8Args(t *testing.T) {
	a := observedWorkerActivity("tool_call", "call", ToolCallEvent{Name: "search_code", Args: "ok\xffbad"})
	if a.Args != "okbad" || !utf8.ValidString(a.Args) {
		t.Fatalf("activity args = %q", a.Args)
	}
}
