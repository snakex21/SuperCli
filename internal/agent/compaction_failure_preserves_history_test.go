package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

func compactionFailureHistory() []llm.Message {
	native := policyReply("original-native-tool-state")
	native.ToolCalls = []llm.ToolCall{{ID: "original-read", Name: "read_lines", Arguments: `{"file":"original.go"}`}}
	return []llm.Message{
		{Role: llm.RoleUser, Content: "Preserve every earlier requirement. " + strings.Repeat("original request evidence ", 2000)},
		native,
		{Role: llm.RoleTool, Name: "read_lines", ToolCallID: "original-read", Content: "1 | original source evidence"},
		{Role: llm.RoleAssistant, Content: "Original investigation completed."},
		{Role: llm.RoleUser, Content: "Retain this previous correction."},
		policyReply("retained-recent-native-state"),
	}
}

func newCompactionFailureLoop(t *testing.T, p llm.Provider, summarize Summarizer, window int, writer SessionWriter) *Loop {
	t.Helper()
	l, err := NewLoop(LoopConfig{
		Provider: p, Registry: tools.NewRegistry(), Writer: writer,
		System: "Standing policy remains unchanged.", InitialMessages: compactionFailureHistory(),
		MaxSteps: 3, PruneProtectTokens: -1, Summarizer: summarize,
		WindowFor:  func(string) int { return window },
		LearnLimit: func(_ string, limit int) { window = limit },
	})
	if err != nil {
		t.Fatal(err)
	}
	// Isolate ordinary threshold/overflow defense from model-handoff policy,
	// which deliberately may continue with full history after helper failure.
	l.contextModel = contextModelState{loaded: true, provider: l.prefillScope(), model: l.modelID}
	return l
}

func compactionFailureJSON(t *testing.T, messages []llm.Message) string {
	t.Helper()
	raw, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func compactionFailureEvents(t *testing.T, l *Loop, ctx context.Context) []Event {
	t.Helper()
	ch, err := l.Run(ctx, "current task")
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	for event := range ch {
		events = append(events, event)
	}
	return events
}

func assertCompactionFailureEvents(t *testing.T, events []Event, want error, text string) {
	t.Helper()
	errorsSeen := 0
	for _, event := range events {
		switch event := event.(type) {
		case ErrorEvent:
			errorsSeen++
			if want != nil && !errors.Is(event.Err, want) {
				t.Fatalf("error=%v, want errors.Is(%v)", event.Err, want)
			}
			if text != "" && !strings.Contains(event.Err.Error(), text) {
				t.Fatalf("error=%v, want contains %q", event.Err, text)
			}
		case DoneEvent, AutoCompactEvent:
			t.Fatalf("failed compaction reported completion or reduction: %+v", event)
		}
	}
	if errorsSeen != 1 {
		t.Fatalf("error events=%d, want 1", errorsSeen)
	}
}

func assertCompactionFailureHistory(t *testing.T, l *Loop, expected string, w *flakyWriter) {
	t.Helper()
	if got := compactionFailureJSON(t, l.Messages); got != expected {
		t.Fatal("failed helper changed canonical messages, recent tail or native evidence")
	}
	if got := compactionFailureJSON(t, l.VisibleMessages()); got != expected || l.HiddenCount() != 0 {
		t.Fatal("failed helper hid messages from the next request")
	}
	if !hasPolicyMarker(l.Messages, "original-native-tool-state") || !hasPolicyMarker(l.Messages, "retained-recent-native-state") {
		t.Fatal("failed helper lost original or retained native state")
	}
	w.mu.Lock()
	projections := len(w.projections)
	w.mu.Unlock()
	if projections != 0 {
		t.Fatalf("failed helper wrote %d model projections", projections)
	}
	if status := l.PersistStatus(); status.Failures != 0 || status.Pending != 0 || status.Dropped != 0 || status.ProjectionDirty || !status.LastWriteOK {
		t.Fatalf("failed helper damaged archive persistence health: %+v", status)
	}
}

func TestConfiguredCompactionFailureStopsRunAndOverflowRetry(t *testing.T) {
	transportErr := errors.New("summary transport unavailable")
	cases := []struct {
		name   string
		script []llm.Delta
		want   error
		text   string
	}{
		{"length", []llm.Delta{{Content: "Goal: misleading partial"}, {FinishReason: "length"}}, nil, "length"},
		{"tool-call", []llm.Delta{{ToolCall: &llm.ToolCall{ID: "unexpected", Name: "read_lines", Arguments: `{}`}}}, nil, "tool call"},
		{"transport", []llm.Delta{{Err: transportErr}}, transportErr, ""},
		{"empty", []llm.Delta{{FinishReason: "stop"}}, nil, "empty text"},
		{"whitespace", []llm.Delta{{Content: " \n\t ", FinishReason: "stop"}}, nil, "empty text"},
		{"instruction-echo", []llm.Delta{{Content: compactionTemplate, FinishReason: "stop"}}, errCompactionInstructionEcho, ""},
	}
	for _, emergency := range []bool{false, true} {
		mode := "automatic"
		if emergency {
			mode = "overflow"
		}
		for _, tc := range cases {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				scripts := [][]llm.Delta{tc.script, {{Content: "must not continue without older requirements", FinishReason: "stop"}}}
				window, wantCalls := 100, int32(1)
				if emergency {
					scripts = append([][]llm.Delta{{{Err: errors.New("context length is 4096 tokens: too many tokens")}}}, scripts...)
					window, wantCalls = 100_000, 2
				}
				p := &stubProvider{name: "fixture", scripts: scripts}
				writer := &flakyWriter{}
				l := newCompactionFailureLoop(t, p, NewAutoSummarizer(nil), window, writer)
				expected := compactionFailureJSON(t, append(append([]llm.Message(nil), l.Messages...), llm.Message{Role: llm.RoleUser, Content: "current task"}))
				events := compactionFailureEvents(t, l, context.Background())
				assertCompactionFailureEvents(t, events, tc.want, tc.text)
				assertCompactionFailureHistory(t, l, expected, writer)
				if atomic.LoadInt32(&p.calls) != wantCalls || len(p.reqs) != int(wantCalls) {
					t.Fatalf("requests=%d, want %d (no main request/retry after failure)", p.calls, wantCalls)
				}
				if p.toolReqs[wantCalls-1] != 0 || len(p.reqs[wantCalls-1]) != 2 {
					t.Fatal("summary request changed its text-only contract")
				}
				if emergency && l.windowResolution().Tokens != 4096 {
					t.Fatal("preserving history lost the learned provider limit")
				}
			})
		}
	}
}

func TestConfiguredCompactionRejectsEmptyAndIneffectiveCallbacks(t *testing.T) {
	for _, reply := range []string{"", " \r\n\t ", strings.Repeat("larger replacement ", 10_000)} {
		for _, emergency := range []bool{false, true} {
			t.Run(map[bool]string{false: "automatic", true: "overflow"}[emergency]+"/"+compactionFailureReplyName(reply), func(t *testing.T) {
				window := 100
				scripts := [][]llm.Delta{{{Content: "must not run after rejected summary", FinishReason: "stop"}}}
				wantRequests := int32(0)
				if emergency {
					window = 100_000
					scripts = append([][]llm.Delta{{{Err: errors.New("context length is 4096 tokens")}}}, scripts...)
					wantRequests = 1
				}
				p := &stubProvider{name: "fixture", scripts: scripts}
				writer, helpers := &flakyWriter{}, 0
				l := newCompactionFailureLoop(t, p, func(context.Context, llm.Provider, []llm.Message) (string, error) {
					helpers++
					return reply, nil
				}, window, writer)
				expected := compactionFailureJSON(t, append(append([]llm.Message(nil), l.Messages...), llm.Message{Role: llm.RoleUser, Content: "current task"}))
				events := compactionFailureEvents(t, l, context.Background())
				text := "empty summary"
				if strings.TrimSpace(reply) != "" {
					text = "insufficient reduction"
				}
				assertCompactionFailureEvents(t, events, nil, text)
				assertCompactionFailureHistory(t, l, expected, writer)
				if helpers != 1 || atomic.LoadInt32(&p.calls) != wantRequests {
					t.Fatalf("helpers=%d provider requests=%d, want 1/%d", helpers, p.calls, wantRequests)
				}
			})
		}
	}
}

func compactionFailureReplyName(reply string) string {
	if reply == "" {
		return "empty"
	}
	if strings.TrimSpace(reply) == "" {
		return "whitespace"
	}
	return "ineffective"
}

func TestConfiguredCompactionCancellationPreservesHistory(t *testing.T) {
	for _, kind := range []string{"provider", "error", "stubborn-success"} {
		for _, emergency := range []bool{false, true} {
			t.Run(map[bool]string{false: "automatic", true: "overflow"}[emergency]+"/"+kind, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				window, wantRequests := 100, int32(0)
				scripts := [][]llm.Delta{{{Content: "Goal: continue. State: keep requirements.", FinishReason: "stop"}}, {{Content: "must not run after cancellation", FinishReason: "stop"}}}
				if kind == "provider" {
					wantRequests++
				}
				if emergency {
					window = 100_000
					wantRequests++
					scripts = append([][]llm.Delta{{{Err: errors.New("context length is 4096 tokens")}}}, scripts...)
				}
				p := &stubProvider{name: "fixture", scripts: scripts}
				if kind == "provider" {
					helperCall := 0
					if emergency {
						helperCall = 1
					}
					p.onCalled = func(call int) {
						if call == helperCall {
							cancel()
						}
					}
				}
				writer, helpers := &flakyWriter{}, 0
				auto := NewAutoSummarizer(nil)
				l := newCompactionFailureLoop(t, p, func(callCtx context.Context, provider llm.Provider, messages []llm.Message) (string, error) {
					helpers++
					if kind == "provider" {
						return auto(callCtx, provider, messages)
					}
					cancel()
					if kind == "stubborn-success" {
						return WrapCompactSummary("Goal: complete. State: keep requirements. Pending: continue."), nil
					}
					return "", context.Canceled
				}, window, writer)
				expected := compactionFailureJSON(t, append(append([]llm.Message(nil), l.Messages...), llm.Message{Role: llm.RoleUser, Content: "current task"}))
				assertCompactionFailureEvents(t, compactionFailureEvents(t, l, ctx), context.Canceled, "")
				assertCompactionFailureHistory(t, l, expected, writer)
				if helpers != 1 || atomic.LoadInt32(&p.calls) != wantRequests {
					t.Fatalf("canceled helper repeated work: helpers=%d requests=%d", helpers, p.calls)
				}
			})
		}
	}
	for _, configured := range []bool{false, true} {
		writer, helpers := &flakyWriter{}, 0
		var summarize Summarizer
		if configured {
			summarize = func(context.Context, llm.Provider, []llm.Message) (string, error) {
				helpers++
				return "unexpected", nil
			}
		}
		l := newCompactionFailureLoop(t, &stubProvider{name: "fixture"}, summarize, 100, writer)
		before := compactionFailureJSON(t, l.Messages)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := l.maybeAutoCompact(ctx, nil, ""); !errors.Is(err, context.Canceled) || helpers != 0 {
			t.Fatalf("pre-canceled defense: err=%v helpers=%d", err, helpers)
		}
		assertCompactionFailureHistory(t, l, before, writer)
		// A canceled Run still retains its accepted raw user request, but must
		// terminate before either summary or main inference and any hiding.
		p := &stubProvider{name: "fixture", scripts: [][]llm.Delta{{{Content: "unexpected", FinishReason: "stop"}}}}
		runWriter := &flakyWriter{}
		runLoop := newCompactionFailureLoop(t, p, summarize, 100, runWriter)
		expected := compactionFailureJSON(t, append(append([]llm.Message(nil), runLoop.Messages...), llm.Message{Role: llm.RoleUser, Content: "current task"}))
		assertCompactionFailureEvents(t, compactionFailureEvents(t, runLoop, ctx), context.Canceled, "")
		assertCompactionFailureHistory(t, runLoop, expected, runWriter)
		if helpers != 0 || p.calls != 0 {
			t.Fatalf("pre-canceled Run performed inference: helpers=%d requests=%d", helpers, p.calls)
		}
	}
}

type compactionFailureDurableWriter struct {
	*session.Writer
	projections int
}

func (w *compactionFailureDurableWriter) SaveContextProjection(ctx context.Context, messages []llm.Message) error {
	w.projections++
	return w.Writer.SaveContextProjection(ctx, messages)
}

func TestConfiguredCompactionFailurePreservesDurableResume(t *testing.T) {
	ctx := context.Background()
	store, err := session.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	sess, err := store.Create(t.TempDir(), "fixture", "failed compaction keeps archive")
	if err != nil {
		t.Fatal(err)
	}
	w := &compactionFailureDurableWriter{Writer: session.NewWriter(store, sess.ID)}
	for _, message := range compactionFailureHistory() {
		if err := w.AppendMessage(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	// Keep an existing durable projection; a failed helper must not overwrite
	// it with placeholders, and resume must still append the new raw request.
	if err := w.Writer.SaveContextProjection(ctx, compactionFailureHistory()); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("synthetic backend failure")
	p := &stubProvider{name: "fixture", scripts: [][]llm.Delta{{{Err: failure}}, {{Content: "must not run", FinishReason: "stop"}}}}
	l := newCompactionFailureLoop(t, p, NewAutoSummarizer(nil), 100, w)
	assertCompactionFailureEvents(t, compactionFailureEvents(t, l, ctx), failure, "")
	if w.projections != 0 || l.HiddenCount() != 0 {
		t.Fatalf("failure wrote projection=%d or hid messages=%d", w.projections, l.HiddenCount())
	}
	want := append(compactionFailureHistory(), llm.Message{Role: llm.RoleUser, Content: "current task"})
	resumed, err := store.ReadModelContext(ctx, sess.ID)
	if err != nil || !reflect.DeepEqual(resumed, want) {
		t.Fatalf("resume after failure lost/duplicated task history: err=%v messages=%d", err, len(resumed))
	}
	rows, err := store.ReadMessages(ctx, sess.ID)
	if err != nil || len(rows) != len(want) {
		t.Fatalf("archive after failure: err=%v rows=%d, want=%d", err, len(rows), len(want))
	}
	for i, row := range rows {
		message, err := row.ToMessage()
		if err != nil || !reflect.DeepEqual(message, want[i]) {
			t.Fatalf("archive row %d changed: err=%v", i, err)
		}
	}
	if status := l.PersistStatus(); !status.LastWriteOK || status.Failures != 0 || status.ProjectionDirty {
		t.Fatalf("archive health changed: %+v", status)
	}
}

func TestConfiguredCompactionSuccessAndLegacyFallbackRemainAvailable(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(map[bool]string{false: "nil-legacy", true: "configured-success"}[configured], func(t *testing.T) {
			p := &stubProvider{name: "fixture", scripts: [][]llm.Delta{{{Content: "Task complete.", FinishReason: "stop"}}}}
			writer, helpers := &flakyWriter{}, 0
			var summarize Summarizer
			wantRequests := int32(1)
			if configured {
				p.scripts = append([][]llm.Delta{{{Content: "Goal: continue. Done: original investigation. State: keep requirements. Pending: current task.", FinishReason: "stop"}}}, p.scripts...)
				wantRequests = 2
				auto := NewAutoSummarizer(nil)
				summarize = func(ctx context.Context, provider llm.Provider, messages []llm.Message) (string, error) {
					helpers++
					return auto(ctx, provider, messages)
				}
			}
			l := newCompactionFailureLoop(t, p, summarize, 100, writer)
			compacted, done := 0, 0
			for _, event := range compactionFailureEvents(t, l, context.Background()) {
				switch event := event.(type) {
				case ErrorEvent:
					t.Fatal(event.Err)
				case AutoCompactEvent:
					compacted++
				case DoneEvent:
					done++
				}
			}
			wantHelpers := 0
			if configured {
				wantHelpers = 1
			}
			if helpers != wantHelpers || p.calls != wantRequests || compacted != 1 || done != 1 {
				t.Fatalf("successful flow changed: helpers=%d requests=%d compact=%d done=%d", helpers, p.calls, compacted, done)
			}
			if (l.HiddenCount() > 0) == configured {
				t.Fatalf("legacy hiding/configured summary changed: hidden=%d configured=%v", l.HiddenCount(), configured)
			}
			if configured && (p.toolReqs[0] != 0 || len(p.reqs[0]) != 2 || p.reqs[0][0].Content != compactionPrompt) {
				t.Fatal("successful helper changed its existing request contract")
			}
			visible := RenderCompactTranscript(l.VisibleMessages())
			if !strings.Contains(visible, "Retain this previous correction.") || !strings.Contains(visible, "current task") {
				t.Fatal("successful defense discarded recent instructions")
			}
		})
	}
}
