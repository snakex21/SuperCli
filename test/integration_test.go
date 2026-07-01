//go:build integration

package test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/tools"
)

// lmStudioBase is the default LM Studio endpoint.
const lmStudioBase = "http://localhost:1234/v1"

// lmStudioAvailable checks if LM Studio is reachable.
func lmStudioAvailable(t *testing.T) (string, bool) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(lmStudioBase + "/models")
	if err != nil {
		t.Skipf("LM Studio not available at %s: %v", lmStudioBase, err)
		return "", false
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Skipf("LM Studio returned %d", resp.StatusCode)
		return "", false
	}
	return lmStudioBase, true
}

// newLMStudioProvider creates an OpenAI-compat provider pointing at LM Studio.
func newLMStudioProvider(t *testing.T, modelID string) llm.Provider {
	t.Helper()
	baseURL, _ := lmStudioAvailable(t)
	p, err := llm.NewOpenAI(llm.OpenAIConfig{
		BaseURL: baseURL,
		Model:   modelID,
		Timeout: 60 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}
	return p
}

// TestIntegration_Streaming verifies that streaming produces
// multiple MessageEvents and a final DoneEvent.
func TestIntegration_Streaming(t *testing.T) {
	baseURL, ok := lmStudioAvailable(t)
	if !ok {
		return
	}
	p, err := llm.NewOpenAI(llm.OpenAIConfig{
		BaseURL: baseURL,
		Model:   "auto", // LM Studio auto-selects loaded model
		Timeout: 60 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ch, err := p.Complete(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: "Reply with exactly 'hello world' in lowercase."},
		{Role: llm.RoleUser, Content: "say hello"},
	}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	var (
		text     strings.Builder
		msgCount int
		done     bool
	)
	for d := range ch {
		if d.Err != nil {
			t.Fatalf("stream error: %v", d.Err)
		}
		if d.FinishReason != "" {
			done = true
		}
		if d.Content != "" {
			text.WriteString(d.Content)
			msgCount++
		}
	}
	if !done {
		t.Error("stream did not finish with a terminal delta")
	}
	if text.Len() == 0 {
		t.Error("no text received")
	}
	if msgCount < 1 {
		t.Error("expected at least 1 MessageEvent (but typically many more)")
	}
	t.Logf("streamed %d deltas, total text: %q", msgCount, text.String())
}

// TestIntegration_ToolCall verifies tool call execution through
// the agent loop.
func TestIntegration_ToolCall(t *testing.T) {
	baseURL, ok := lmStudioAvailable(t)
	if !ok {
		return
	}
	p, err := llm.NewOpenAI(llm.OpenAIConfig{
		BaseURL: baseURL,
		Model:   "auto",
		Timeout: 60 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}

	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name:        "echo",
		Description: "Returns the input unchanged.",
		Fn: func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: string(args)}, nil
		},
	})

	loop, err := agent.NewLoop(agent.LoopConfig{
		Provider: p,
		Registry: reg,
		MaxSteps: 3,
		System:   "You are a test assistant. Be concise.",
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ch, err := loop.Run(ctx, "Say hello in one word")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var (
		hasMessage bool
		done       bool
	)
	for ev := range ch {
		switch e := ev.(type) {
		case agent.MessageEvent:
			hasMessage = true
			t.Logf("msg: %s", e.Text)
		case agent.DoneEvent:
			done = true
		case agent.ErrorEvent:
			t.Fatalf("ErrorEvent: %v", e.Err)
		}
	}
	if !hasMessage {
		t.Error("no MessageEvent received")
	}
	if !done {
		t.Error("no DoneEvent received")
	}
}

// toolListRecorder wraps a Provider and records the tool names
// passed to every Complete call, so a test can assert the request
// `tools` list stayed stable across the whole run (the KV-cache
// guarantee of the stableToolset mode).
type toolListRecorder struct {
	llm.Provider
	mu    sync.Mutex
	calls [][]string
}

func (r *toolListRecorder) Complete(ctx context.Context, msgs []llm.Message, tools []llm.ToolDef) (<-chan llm.Delta, error) {
	names := make([]string, 0, len(tools))
	for _, td := range tools {
		names = append(names, td.Name)
	}
	r.mu.Lock()
	r.calls = append(r.calls, names)
	r.mu.Unlock()
	return r.Provider.Complete(ctx, msgs, tools)
}

// TestIntegration_StableToolset_ToolSearchThenTailCall is the live
// test for the stable-toolset KV-cache mode: the model must (1) call
// tool_search to discover a dormant tail tool, (2) call that tool by
// name even though it was never promoted into the request `tools`
// list, and (3) finish — while every provider request carries the
// exact same tools list (no growth = no prompt-cache invalidation).
func TestIntegration_StableToolset_ToolSearchThenTailCall(t *testing.T) {
	baseURL, ok := lmStudioAvailable(t)
	if !ok {
		return
	}
	inner, err := llm.NewOpenAI(llm.OpenAIConfig{
		BaseURL: baseURL,
		Model:   "auto",
		Timeout: 120 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}
	rec := &toolListRecorder{Provider: inner}

	reg := tools.NewRegistry()
	// Dormant tail tool: NOT always-on, reachable only via tool_search.
	reg.MustRegister(tools.Tool{
		Name:        "reverse_text",
		Description: "Reverses the characters of the given text string and returns the reversed text.",
		Schema:      `{"type":"object","properties":{"text":{"type":"string","description":"the text to reverse"}},"required":["text"]}`,
		Fn: func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			var a struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return tools.Result{Err: err}, nil
			}
			r := []rune(a.Text)
			for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
				r[i], r[j] = r[j], r[i]
			}
			return tools.Result{Text: string(r)}, nil
		},
	})
	idx, err := tools.NewInMemoryIndex()
	if err != nil {
		t.Fatalf("NewInMemoryIndex: %v", err)
	}
	defer idx.Close()
	searcher := tools.NewToolSearcher(reg, idx)
	reg.MustRegister(searcher.Spec())
	reg.MarkAlwaysOn("tool_search")
	if err := searcher.RebuildIndex(); err != nil {
		t.Fatalf("RebuildIndex: %v", err)
	}

	loop, err := agent.NewLoop(agent.LoopConfig{
		Provider:      rec,
		Registry:      reg,
		MaxSteps:      8,
		System:        "You are a test assistant with tools. Follow instructions exactly.",
		ThinTools:     true,
		StableToolset: true,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	ch, err := loop.Run(ctx, "Use tool_search to find a tool that reverses text, then call that tool with the text 'kotek' and tell me the exact result.")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var (
		searchedTool  bool
		calledReverse bool
		reverseOutput string
		finalText     strings.Builder
		done          bool
	)
	for ev := range ch {
		switch e := ev.(type) {
		case agent.ToolCallEvent:
			t.Logf("tool call: %s %s", e.Name, e.Args)
			if e.Name == "tool_search" {
				searchedTool = true
			}
			if e.Name == "reverse_text" {
				calledReverse = true
			}
		case agent.ToolResultEvent:
			if e.Err != nil {
				t.Logf("tool result err: %v", e.Err)
			} else if calledReverse && reverseOutput == "" && e.Output == "ketok" {
				reverseOutput = e.Output
			}
		case agent.MessageEvent:
			finalText.WriteString(e.Text)
		case agent.DoneEvent:
			done = true
		case agent.ErrorEvent:
			t.Fatalf("ErrorEvent: %v", e.Err)
		}
	}
	t.Logf("final text: %q", finalText.String())

	if !searchedTool {
		t.Error("model never called tool_search")
	}
	if !calledReverse {
		t.Error("model never called the tail tool reverse_text")
	}
	if reverseOutput != "ketok" {
		t.Errorf("reverse_text output = %q, want %q", reverseOutput, "ketok")
	}
	if !done {
		t.Error("no DoneEvent — run did not finish cleanly")
	}

	// The stable-toolset invariant: every request carried the exact
	// same tools list, and reverse_text never entered it.
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.calls) == 0 {
		t.Fatal("no provider calls recorded")
	}
	first := strings.Join(rec.calls[0], ",")
	for i, names := range rec.calls {
		if got := strings.Join(names, ","); got != first {
			t.Errorf("request %d tools list drifted: %q != %q (KV prompt cache invalidated)", i, got, first)
		}
		for _, n := range names {
			if n == "reverse_text" {
				t.Errorf("request %d: reverse_text was promoted into the tools list", i)
			}
		}
	}
	t.Logf("stable tools list across %d provider calls: %s", len(rec.calls), first)
}

// TestIntegration_MultiTurn verifies multi-turn conversation
// (the agent remembers context between turns).
func TestIntegration_MultiTurn(t *testing.T) {
	baseURL, ok := lmStudioAvailable(t)
	if !ok {
		return
	}
	p, err := llm.NewOpenAI(llm.OpenAIConfig{
		BaseURL: baseURL,
		Model:   "auto",
		Timeout: 60 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}

	reg := tools.NewRegistry()
	loop, err := agent.NewLoop(agent.LoopConfig{
		Provider: p,
		Registry: reg,
		MaxSteps: 3,
		System:   "You are a test assistant. Reply with exactly what is asked.",
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}

	ctx := context.Background()

	// Turn 1
	ch1, err := loop.Run(ctx, "My name is Alice. Reply with 'OK'.")
	if err != nil {
		t.Fatalf("Run 1: %v", err)
	}
	drainEvents(t, ch1)

	// Turn 2 — model should remember the name.
	ch2, err := loop.Run(ctx, "What is my name? Reply with just the name.")
	if err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	var text2 strings.Builder
	for ev := range ch2 {
		if e, ok := ev.(agent.MessageEvent); ok {
			text2.WriteString(e.Text)
		}
		if _, ok := ev.(agent.ErrorEvent); ok {
			t.Fatal("error in turn 2")
		}
	}
	result := strings.ToLower(text2.String())
	t.Logf("turn 2 response: %q", result)
	if !strings.Contains(result, "alice") {
		t.Errorf("model did not remember name 'Alice', got: %q", result)
	}
}

// TestIntegration_CancelMidStream verifies that cancelling the
// context mid-stream stops the provider cleanly.
func TestIntegration_CancelMidStream(t *testing.T) {
	baseURL, ok := lmStudioAvailable(t)
	if !ok {
		return
	}
	p, err := llm.NewOpenAI(llm.OpenAIConfig{
		BaseURL: baseURL,
		Model:   "auto",
		Timeout: 60 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := p.Complete(ctx, []llm.Message{
		{Role: llm.RoleUser, Content: "Write a very long essay about the history of Poland. Each paragraph should be at least 50 words. Continue until I say stop."},
	}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// Read a few deltas then cancel.
	count := 0
	for d := range ch {
		if d.Err != nil {
			// Context cancellation is expected.
			if ctx.Err() != nil {
				t.Logf("stream cancelled after %d deltas (expected)", count)
				return
			}
			t.Fatalf("unexpected stream error: %v", d.Err)
		}
		count++
		if count >= 5 {
			cancel()
		}
	}
	// If we get here, the channel closed without error after cancel.
	t.Logf("stream ended after %d deltas (cancel propagated)", count)
}

// TestIntegration_Timeout verifies that a short timeout causes
// the provider to fail cleanly with a context error.
func TestIntegration_Timeout(t *testing.T) {
	baseURL, ok := lmStudioAvailable(t)
	if !ok {
		return
	}
	p, err := llm.NewOpenAI(llm.OpenAIConfig{
		BaseURL: baseURL,
		Model:   "auto",
		Timeout: 1 * time.Second, // very short
	})
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	// Complete should fail quickly because ctx is already expired.
	_, err = p.Complete(ctx, []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
	}, nil)
	if err == nil {
		t.Error("expected error from expired context, got nil")
	} else {
		t.Logf("timeout error (expected): %v", err)
	}
}

// TestIntegration_ConcurrentSessions runs N parallel sessions
// against the same LM Studio instance to check for races.
func TestIntegration_ConcurrentSessions(t *testing.T) {
	baseURL, ok := lmStudioAvailable(t)
	if !ok {
		return
	}

	const sessions = 10
	var wg sync.WaitGroup
	errs := make(chan error, sessions)

	for i := 0; i < sessions; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			p, err := llm.NewOpenAI(llm.OpenAIConfig{
				BaseURL: baseURL,
				Model:   "auto",
				Timeout: 30 * time.Second,
			})
			if err != nil {
				errs <- fmt.Errorf("session %d: %w", id, err)
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			ch, err := p.Complete(ctx, []llm.Message{
				{Role: llm.RoleUser, Content: "Say 'hello' in one word."},
			}, nil)
			if err != nil {
				errs <- fmt.Errorf("session %d Complete: %w", id, err)
				return
			}
			for d := range ch {
				if d.Err != nil {
					errs <- fmt.Errorf("session %d stream: %w", id, d.Err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	for e := range errs {
		t.Error(e)
	}
}

func drainEvents(t *testing.T, ch <-chan agent.Event) {
	t.Helper()
	for ev := range ch {
		if e, ok := ev.(agent.ErrorEvent); ok {
			t.Errorf("unexpected ErrorEvent: %v", e.Err)
		}
	}
}
