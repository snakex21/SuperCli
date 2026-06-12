package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// stubProvider emits a scripted set of deltas per call. Once the
// caller index exceeds len(scripts), the LAST script is replayed.
// This lets a test declare the early turns explicitly and have
// later turns behave however the final script says — useful for
// "always tool_calls" loops that should hit MaxSteps.
type stubProvider struct {
	name     string
	scripts  [][]llm.Delta // one script per call
	calls    int32
	onCalled func(call int)
}

func (p *stubProvider) Name() string { return p.name }

func (p *stubProvider) SupportsVision() bool { return true }

func (p *stubProvider) Complete(ctx context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(p.scripts) == 0 {
		ch := make(chan llm.Delta, 1)
		go func() {
			defer close(ch)
			ch <- llm.Delta{FinishReason: "stop", Usage: &llm.Usage{Total: 1}}
		}()
		return ch, nil
	}
	callNum := int(atomic.AddInt32(&p.calls, 1)) - 1
	idx := callNum
	if idx >= len(p.scripts) {
		idx = len(p.scripts) - 1
	}
	if p.onCalled != nil {
		p.onCalled(callNum)
	}
	script := p.scripts[idx]
	ch := make(chan llm.Delta, len(script)+1)
	go func() {
		defer close(ch)
		for _, d := range script {
			if err := ctx.Err(); err != nil {
				return
			}
			select {
			case ch <- d:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

type captureProvider struct {
	name      string
	messages  []llm.Message
	toolCount int
}

func (p *captureProvider) Name() string         { return p.name }
func (p *captureProvider) SupportsVision() bool { return true }
func (p *captureProvider) Complete(ctx context.Context, msgs []llm.Message, tools []llm.ToolDef) (<-chan llm.Delta, error) {
	p.messages = append([]llm.Message(nil), msgs...)
	p.toolCount = len(tools)
	ch := make(chan llm.Delta, 2)
	go func() {
		defer close(ch)
		ch <- llm.Delta{Content: "hej"}
		ch <- llm.Delta{FinishReason: "stop", Usage: &llm.Usage{Input: 1, Output: 1, Total: 2}}
	}()
	return ch, nil
}

type navigatorProvider struct {
	name      string
	calls     int
	messages  []llm.Message
	toolCount int
}

func (p *navigatorProvider) Name() string         { return p.name }
func (p *navigatorProvider) SupportsVision() bool { return true }
func (p *navigatorProvider) Complete(ctx context.Context, msgs []llm.Message, tools []llm.ToolDef) (<-chan llm.Delta, error) {
	p.calls++
	p.messages = append([]llm.Message(nil), msgs...)
	p.toolCount = len(tools)
	content := "advisor answer"
	if p.calls == 1 {
		content = `{"mode":"advisor","reason":"general conceptual advice"}`
	}
	ch := make(chan llm.Delta, 2)
	go func() {
		defer close(ch)
		ch <- llm.Delta{Content: content}
		ch <- llm.Delta{FinishReason: "stop", Usage: &llm.Usage{Input: 1, Output: 1, Total: 2}}
	}()
	return ch, nil
}

func TestLoop_ChatOnlyRouteSendsShortPromptAndNoTools(t *testing.T) {
	p := &captureProvider{name: "capture"}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name:        "expensive_tool",
		Description: "large schema",
		Schema:      `{"type":"object","properties":{"x":{"type":"string"}}}`,
		Fn: func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "x"}, nil
		},
	})
	reg.MarkAlwaysOn("expensive_tool")
	l := makeLoop(t, p, reg, "FULL COORDINATOR PROMPT THAT SHOULD NOT BE SENT")
	l.navigate = true
	ch, _ := l.Run(context.Background(), "cześć")
	drainEvents(t, ch)

	if p.toolCount != 0 {
		t.Fatalf("toolCount=%d, want 0", p.toolCount)
	}
	if len(p.messages) == 0 || p.messages[0].Role != llm.RoleSystem {
		t.Fatalf("messages = %+v", p.messages)
	}
	if p.messages[0].Content != chatOnlySystemPrompt {
		t.Fatalf("system prompt = %q, want chat-only prompt", p.messages[0].Content)
	}
	for _, m := range p.messages {
		if strings.Contains(m.Content, "FULL COORDINATOR") {
			t.Fatalf("full prompt leaked into chat-only messages: %+v", p.messages)
		}
	}
}

func TestLoop_NavigatorCanChooseAdvisorWithoutTools(t *testing.T) {
	p := &navigatorProvider{name: "navigator"}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name:        "expensive_tool",
		Description: "large schema",
		Schema:      `{"type":"object"}`,
		Fn: func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "x"}, nil
		},
	})
	reg.MarkAlwaysOn("expensive_tool")
	l := makeLoop(t, p, reg, "FULL COORDINATOR PROMPT")
	l.navigate = true
	ch, _ := l.Run(context.Background(), "co lepsze?")
	drainEvents(t, ch)

	if p.calls != 2 {
		t.Fatalf("provider calls = %d, want navigator + answer", p.calls)
	}
	if p.toolCount != 0 {
		t.Fatalf("toolCount=%d, want 0", p.toolCount)
	}
	if len(p.messages) == 0 || p.messages[0].Content != advisorSystemPrompt {
		t.Fatalf("final messages = %+v, want advisor prompt", p.messages)
	}
}

func echoProvider(name string) *stubProvider {
	return &stubProvider{
		name: name,
		scripts: [][]llm.Delta{{
			{Role: llm.RoleAssistant, Content: "hi"},
			{Content: " there"},
			{FinishReason: "stop", Usage: &llm.Usage{Input: 5, Output: 2, Total: 7}},
		}},
	}
}

func makeLoop(t *testing.T, p llm.Provider, reg *tools.Registry, sys string) *Loop {
	t.Helper()
	l, err := NewLoop(LoopConfig{
		Provider: p,
		Registry: reg,
		System:   sys,
		MaxSteps: 5,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	return l
}

func drainEvents(t *testing.T, ch <-chan Event) []Event {
	t.Helper()
	var out []Event
	for ev := range ch {
		out = append(out, ev)
	}
	return out
}

func TestNewLoop_RequiresProvider(t *testing.T) {
	if _, err := NewLoop(LoopConfig{Registry: tools.NewRegistry()}); err == nil {
		t.Fatal("expected error on nil provider")
	}
}

func TestNewLoop_RequiresRegistry(t *testing.T) {
	if _, err := NewLoop(LoopConfig{Provider: echoProvider("p")}); err == nil {
		t.Fatal("expected error on nil registry")
	}
}

func TestLoop_TextOnlyRun_EmitsMessageAndDone(t *testing.T) {
	reg := tools.NewRegistry()
	l := makeLoop(t, echoProvider("m"), reg, "you are helpful")
	ch, err := l.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := drainEvents(t, ch)

	var got []MessageEvent
	var done *DoneEvent
	for _, e := range events {
		switch v := e.(type) {
		case MessageEvent:
			got = append(got, v)
		case DoneEvent:
			done = &v
		case ErrorEvent:
			t.Fatalf("unexpected error: %v", v.Err)
		}
	}
	if len(got) != 2 {
		t.Fatalf("MessageEvents = %d, want 2", len(got))
	}
	if got[0].Text != "hi" || got[1].Text != " there" {
		t.Fatalf("text chunks = %+v", got)
	}
	if done == nil {
		t.Fatal("no DoneEvent")
	}
	if done.Usage.Total != 7 {
		t.Fatalf("usage.Total = %d, want 7", done.Usage.Total)
	}
}

func TestLoop_RejectsEmptyPrompt(t *testing.T) {
	l := makeLoop(t, echoProvider("m"), tools.NewRegistry(), "")
	if _, err := l.Run(context.Background(), ""); err == nil {
		t.Fatal("expected error on empty prompt")
	}
}

func TestLoop_AppendsUserMessage(t *testing.T) {
	l := makeLoop(t, echoProvider("m"), tools.NewRegistry(), "")
	ch, _ := l.Run(context.Background(), "hi there")
	drainEvents(t, ch)
	if len(l.Messages) < 2 {
		t.Fatalf("Messages len = %d, want >= 2", len(l.Messages))
	}
	if l.Messages[0].Role != llm.RoleSystem && l.Messages[0].Role == "" {
		// no system in this loop
	}
	last := l.Messages[len(l.Messages)-1]
	if last.Role != llm.RoleAssistant {
		t.Fatalf("last msg role = %q, want assistant", last.Role)
	}
}

func TestLoop_ToolCallAndContinue(t *testing.T) {
	// First call: model returns a tool call.
	// Second call: model returns text and stops.
	p := &stubProvider{
		name: "m",
		scripts: [][]llm.Delta{
			{
				{Role: llm.RoleAssistant},
				{ToolCall: &llm.ToolCall{ID: "call_1", Name: "echo", Arguments: `{"msg":"hi"}`}},
				{FinishReason: "tool_calls", Usage: &llm.Usage{Input: 1, Output: 1, Total: 2}},
			},
			{
				{Role: llm.RoleAssistant, Content: "ok"},
				{FinishReason: "stop", Usage: &llm.Usage{Input: 2, Output: 1, Total: 3}},
			},
		},
	}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name:        "echo",
		Description: "echo",
		Schema:      `{"type":"object","properties":{"msg":{"type":"string"}}}`,
		Fn: func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			var p struct {
				Msg string `json:"msg"`
			}
			_ = json.Unmarshal(args, &p)
			return tools.Result{Text: "echoed: " + p.Msg}, nil
		},
	})

	l := makeLoop(t, p, reg, "")
	ch, err := l.Run(context.Background(), "call echo")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := drainEvents(t, ch)

	var (
		msgs    []MessageEvent
		calls   []ToolCallEvent
		results []ToolResultEvent
		done    *DoneEvent
		errEv   *ErrorEvent
	)
	for _, e := range events {
		switch v := e.(type) {
		case MessageEvent:
			msgs = append(msgs, v)
		case ToolCallEvent:
			calls = append(calls, v)
		case ToolResultEvent:
			results = append(results, v)
		case DoneEvent:
			done = &v
		case ErrorEvent:
			errEv = &v
		}
	}
	if errEv != nil {
		t.Fatalf("unexpected error: %v", errEv.Err)
	}
	if len(calls) != 1 || calls[0].Name != "echo" {
		t.Fatalf("calls = %+v", calls)
	}
	if len(results) != 1 || results[0].Output != "echoed: hi" {
		t.Fatalf("results = %+v", results)
	}
	if len(msgs) != 1 || msgs[0].Text != "ok" {
		t.Fatalf("msgs = %+v", msgs)
	}
	if done == nil {
		t.Fatal("no DoneEvent")
	}
	if done.Usage.Total != 5 {
		t.Fatalf("usage.Total = %d, want 5 (sum of two turns)", done.Usage.Total)
	}
	if len(l.Messages) != 4 {
		// system-less: user, assistant(tool), tool result, assistant(text) -> 4
		t.Fatalf("Messages len = %d, want 4", len(l.Messages))
	}
	// Verify the tool result message in history references the right call id.
	toolMsg := l.Messages[2]
	if toolMsg.Role != llm.RoleTool || toolMsg.ToolCallID != "call_1" {
		t.Fatalf("tool msg = %+v", toolMsg)
	}
}

func TestLoop_UnknownTool_ProducesErrorMessage(t *testing.T) {
	p := &stubProvider{
		name: "m",
		scripts: [][]llm.Delta{
			{
				{Role: llm.RoleAssistant},
				{ToolCall: &llm.ToolCall{ID: "x", Name: "missing", Arguments: `{}`}},
				{FinishReason: "tool_calls"},
			},
			{
				{Role: llm.RoleAssistant, Content: "done"},
				{FinishReason: "stop"},
			},
		},
	}
	reg := tools.NewRegistry()
	l := makeLoop(t, p, reg, "")
	ch, _ := l.Run(context.Background(), "go")
	events := drainEvents(t, ch)
	var sawErrResult bool
	for _, e := range events {
		if tr, ok := e.(ToolResultEvent); ok && tr.Err != nil {
			sawErrResult = true
		}
	}
	if !sawErrResult {
		t.Fatal("expected ToolResultEvent with Err")
	}
}

func TestLoop_TaskToolCallsRunInParallel(t *testing.T) {
	p := &stubProvider{
		name: "m",
		scripts: [][]llm.Delta{
			{
				{Role: llm.RoleAssistant},
				{ToolCall: &llm.ToolCall{ID: "task_1", Name: "task", Arguments: `{"agent":"explore","prompt":"a"}`}},
				{ToolCall: &llm.ToolCall{ID: "task_2", Name: "task", Arguments: `{"agent":"explore","prompt":"b"}`}},
				{FinishReason: "tool_calls"},
			},
			{
				{Role: llm.RoleAssistant, Content: "done"},
				{FinishReason: "stop"},
			},
		},
	}
	reg := tools.NewRegistry()
	var calls atomic.Int32
	reg.MustRegister(tools.Tool{
		Name:        "task",
		Description: "spawn worker",
		Schema:      `{"type":"object"}`,
		Fn: func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			calls.Add(1)
			select {
			case <-time.After(120 * time.Millisecond):
				return tools.Result{Text: "worker done"}, nil
			case <-ctx.Done():
				return tools.Result{Err: ctx.Err()}, nil
			}
		},
	})

	l := makeLoop(t, p, reg, "")
	start := time.Now()
	ch, _ := l.Run(context.Background(), "launch workers")
	events := drainEvents(t, ch)
	elapsed := time.Since(start)

	if calls.Load() != 2 {
		t.Fatalf("task calls = %d, want 2", calls.Load())
	}
	if elapsed >= 220*time.Millisecond {
		t.Fatalf("task calls appear sequential, elapsed=%s", elapsed)
	}
	var results int
	for _, e := range events {
		if tr, ok := e.(ToolResultEvent); ok && tr.Output == "worker done" {
			results++
		}
	}
	if results != 2 {
		t.Fatalf("tool results = %d, want 2", results)
	}
	if len(l.Messages) < 4 || l.Messages[2].ToolCallID != "task_1" || l.Messages[3].ToolCallID != "task_2" {
		t.Fatalf("tool result messages not appended in call order: %+v", l.Messages)
	}
}

func TestLoop_ContextCancelStopsLoop(t *testing.T) {
	p := &stubProvider{name: "m", scripts: [][]llm.Delta{{{FinishReason: "stop"}}}}
	reg := tools.NewRegistry()
	l := makeLoop(t, p, reg, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ch, _ := l.Run(ctx, "x")
	events := drainEvents(t, ch)
	if len(events) == 0 {
		t.Fatal("expected at least one event on cancel")
	}
	last := events[len(events)-1]
	if _, ok := last.(ErrorEvent); !ok {
		t.Fatalf("last event = %T, want ErrorEvent", last)
	}
}

func TestLoop_ToolErrorReturnedAsToolMessage(t *testing.T) {
	toolErr := errors.New("boom")
	p := &stubProvider{
		name: "m",
		scripts: [][]llm.Delta{
			{
				{Role: llm.RoleAssistant},
				{ToolCall: &llm.ToolCall{ID: "id1", Name: "fail", Arguments: `{}`}},
				{FinishReason: "tool_calls"},
			},
			{
				{Role: llm.RoleAssistant, Content: "ok"},
				{FinishReason: "stop"},
			},
		},
	}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name:        "fail",
		Description: "fail",
		Schema:      "{}",
		Fn: func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			return tools.Result{Err: toolErr}, nil
		},
	})
	l := makeLoop(t, p, reg, "")
	ch, _ := l.Run(context.Background(), "x")
	drainEvents(t, ch)
	// Tool message in history contains "error: boom"
	found := false
	for _, m := range l.Messages {
		if m.Role == llm.RoleTool && contains(m.Content, "boom") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected tool error to appear in history")
	}
}

func TestLoop_MaxStepsEmitsError(t *testing.T) {
	// Provider always returns a tool call so the loop never stops naturally.
	p := &stubProvider{
		name: "m",
		scripts: [][]llm.Delta{{
			{Role: llm.RoleAssistant},
			{ToolCall: &llm.ToolCall{ID: "loop", Name: "noop", Arguments: `{}`}},
			{FinishReason: "tool_calls"},
		}},
	}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name: "noop", Description: "n", Schema: "{}",
		Fn: func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "x"}, nil
		},
	})
	l := makeLoop(t, p, reg, "")
	ch, _ := l.Run(context.Background(), "x")
	events := drainEvents(t, ch)
	last := events[len(events)-1]
	errEv, ok := last.(ErrorEvent)
	if !ok {
		t.Fatalf("last = %T, want ErrorEvent", last)
	}
	if !contains(errEv.Err.Error(), "max steps") {
		t.Fatalf("err = %v, want max steps", errEv.Err)
	}
}

func TestLoop_ToolImageAttachedAsUserMessage(t *testing.T) {
	p := &stubProvider{
		name: "m",
		scripts: [][]llm.Delta{
			{
				{Role: llm.RoleAssistant},
				{ToolCall: &llm.ToolCall{ID: "c1", Name: "snap", Arguments: `{}`}},
				{FinishReason: "tool_calls"},
			},
			{
				{Role: llm.RoleAssistant, Content: "saw it"},
				{FinishReason: "stop"},
			},
		},
	}
	reg := tools.NewRegistry()
	png := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0}
	reg.MustRegister(tools.Tool{
		Name: "snap", Description: "n", Schema: "{}",
		Fn: func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			return tools.Result{
				Text:  "captured",
				Image: &tools.ImageContent{MediaType: "image/png", Data: png},
			}, nil
		},
	})
	l := makeLoop(t, p, reg, "")
	ch, _ := l.Run(context.Background(), "snap")
	drainEvents(t, ch)

	// History should now contain a user message with the image.
	var imgMsg *llm.Message
	for i := range l.Messages {
		m := &l.Messages[i]
		if m.Role == llm.RoleUser && m.HasImage() {
			imgMsg = m
		}
	}
	if imgMsg == nil {
		t.Fatal("expected user message with image part")
	}
	if imgMsg.Parts[1].Image.MediaType != "image/png" {
		t.Fatalf("MediaType = %q", imgMsg.Parts[1].Image.MediaType)
	}
}

func TestLoop_StreamDeltaErrorPropagates(t *testing.T) {
	p := &stubProvider{
		name: "m",
		scripts: [][]llm.Delta{{
			{Content: "half"},
			{Err: errors.New("upstream broke")},
		}},
	}
	l := makeLoop(t, p, tools.NewRegistry(), "")
	ch, _ := l.Run(context.Background(), "x")
	events := drainEvents(t, ch)
	if len(events) == 0 {
		t.Fatal("expected events")
	}
	last := events[len(events)-1]
	if _, ok := last.(ErrorEvent); !ok {
		t.Fatalf("last = %T, want ErrorEvent", last)
	}
}

func TestLoop_SystemPromptPrepended(t *testing.T) {
	p := &stubProvider{
		name: "m",
		scripts: [][]llm.Delta{{
			{Content: "x"},
			{FinishReason: "stop"},
		}},
	}
	l, err := NewLoop(LoopConfig{
		Provider: p,
		Registry: tools.NewRegistry(),
		System:   "be terse",
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	if l.Messages[0].Role != llm.RoleSystem || l.Messages[0].Content != "be terse" {
		t.Fatalf("first msg = %+v", l.Messages[0])
	}
}

func TestLoop_RunContextDeadline(t *testing.T) {
	p := &stubProvider{name: "m", scripts: nil} // produces a stop on each call
	reg := tools.NewRegistry()
	l := makeLoop(t, p, reg, "")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	ch, _ := l.Run(ctx, "x")
	// Should not hang; the events stream should close.
	done := make(chan struct{})
	go func() { drainEvents(t, ch); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not finish in time")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
