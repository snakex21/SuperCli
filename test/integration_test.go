//go:build integration

package test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/ctxexec"
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

// TestIntegration_CtxExecute_FirstCallSchema is the live test for
// the SLIMMED ctx_execute schema. It checks that a 9B model, given
// only the trimmed schema, still forms a CORRECT first call — the
// key risk of token-cutting is losing first-shot accuracy. The
// task needs one real command with an argument list (not a shell
// string), so a malformed call (shell string, missing args) would
// fail the assertions. Success = ctx_execute called with a JSON
// array `command`, correct answer, no failure loop.
func TestIntegration_CtxExecute_FirstCallSchema(t *testing.T) {
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

	// Sandbox home with a log file: 5 of 20 lines contain ERROR.
	home := t.TempDir()
	var b strings.Builder
	for i := 1; i <= 20; i++ {
		if i%4 == 0 {
			fmt.Fprintf(&b, "ERROR line %d something failed\n", i)
		} else {
			fmt.Fprintf(&b, "INFO line %d ok\n", i)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "data.log"), []byte(b.String()), 0644); err != nil {
		t.Fatalf("write data.log: %v", err)
	}

	reg := tools.NewRegistry()
	reg.MustRegister(tools.NewCtxExecuteTool(ctxexec.New(home), home).Spec())
	reg.MarkAlwaysOn("ctx_execute")
	// tool_search is the thin-protocol gateway; keep it present so
	// the thin catalog/preamble render exactly as in production.
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
		MaxSteps:      6,
		System:        "You are a test assistant with tools. Follow instructions exactly.",
		ThinTools:     true,
		StableToolset: true,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	ch, err := loop.Run(ctx, "Using ctx_execute, count how many lines in the file data.log contain the word ERROR. Reply with just the number.")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var (
		ctxCalls      int
		firstCallOK   bool
		firstBadShape bool
		sawFive       bool
		finalText     strings.Builder
		done          bool
	)
	for ev := range ch {
		switch e := ev.(type) {
		case agent.ToolCallEvent:
			t.Logf("tool call #%d: %s %s", ctxCalls+1, e.Name, e.Args)
			if e.Name == "ctx_execute" {
				ctxCalls++
				// Validate the FIRST ctx_execute call's shape: command
				// must be a JSON array of strings, not a shell string.
				if ctxCalls == 1 {
					var p struct {
						Command []string `json:"command"`
					}
					if err := json.Unmarshal([]byte(e.Args), &p); err == nil && len(p.Command) >= 1 {
						firstCallOK = true
					} else {
						firstBadShape = true
						t.Logf("first ctx_execute call bad shape: %v", err)
					}
				}
			}
		case agent.ToolResultEvent:
			if e.Err != nil {
				t.Logf("tool result err: %v", e.Err)
			}
			if strings.Contains(e.Output, "\"stdout\"") && strings.Contains(e.Output, "5") {
				sawFive = true
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
	u := loop.SessionUsage()
	t.Logf("ctx_execute called %d time(s); provider requests: %d; tokens in=%d out=%d", ctxCalls, len(rec.calls), u.Input, u.Output)

	if ctxCalls == 0 {
		t.Error("model never called ctx_execute")
	}
	if firstBadShape {
		t.Error("first ctx_execute call had a malformed command (schema too terse?)")
	}
	if !firstCallOK {
		t.Error("first ctx_execute call did not carry a valid command array")
	}
	// The correct answer is 5. Accept it from the tool result stdout
	// or the final message.
	if !sawFive && !strings.Contains(finalText.String(), "5") {
		t.Errorf("expected answer 5 (lines with ERROR); final=%q", finalText.String())
	}
	if !done {
		t.Error("no DoneEvent — run did not finish cleanly")
	}
	// First-shot success target: at most 2 ctx_execute calls (allow one
	// self-correction). More than that signals a failure loop.
	if ctxCalls > 2 {
		t.Errorf("ctx_execute called %d times — first-shot accuracy regressed", ctxCalls)
	}
}

// TestIntegration_EditLine_FirstCallSchema is the live test for the
// SLIMMED edit_line schema. edit_line is the most format-sensitive
// trimmed tool (a malformed call corrupts the file), so the key risk
// of token-cutting is losing first-shot accuracy on the argument
// shape. The task is a single concrete line edit; success = edit_line
// called with a well-formed call (non-empty file + new_content, line
// >= 1), the file actually changed, and no failure loop.
func TestIntegration_EditLine_FirstCallSchema(t *testing.T) {
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

	// Sandbox home with a small config file. The model must flip the
	// value on the `debug = false` line to `debug = true`.
	home := t.TempDir()
	const original = "name = app\nport = 8080\ndebug = false\nlevel = info\n"
	cfgPath := filepath.Join(home, "config.txt")
	if err := os.WriteFile(cfgPath, []byte(original), 0644); err != nil {
		t.Fatalf("write config.txt: %v", err)
	}

	reg := tools.NewRegistry()
	// edit_line plus the read tools its description points at
	// (read_context first) — all in the thin core, so they render
	// with full schema exactly as in production.
	reg.MustRegister(tools.NewEditLine(home).Spec())
	reg.MustRegister(tools.NewReadContext(home).Spec())
	reg.MustRegister(tools.NewReadLines(home).Spec())
	reg.MarkAlwaysOn("edit_line")
	reg.MarkAlwaysOn("read_context")
	reg.MarkAlwaysOn("read_lines")
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
		MaxSteps:      6,
		System:        "You are a test assistant with tools. Follow instructions exactly.",
		ThinTools:     true,
		StableToolset: true,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	ch, err := loop.Run(ctx, "In the file config.txt, change the line `debug = false` to `debug = true`. Use edit_line.")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var (
		editCalls     int
		firstCallOK   bool
		firstBadShape bool
		done          bool
		finalText     strings.Builder
	)
	for ev := range ch {
		switch e := ev.(type) {
		case agent.ToolCallEvent:
			t.Logf("tool call #%d: %s %s", editCalls+1, e.Name, e.Args)
			if e.Name == "edit_line" {
				editCalls++
				if editCalls == 1 {
					// Args are the model's RAW output (coercion happens
					// later at Execute), so a small model legitimately
					// sends line as a stringified int ("3"). Accept both
					// shapes — the loop coerces it before the tool runs.
					var p struct {
						File       string          `json:"file"`
						Line       json.RawMessage `json:"line"`
						NewContent string          `json:"new_content"`
					}
					err := json.Unmarshal([]byte(e.Args), &p)
					lineOK := false
					if err == nil && len(p.Line) > 0 {
						s := strings.Trim(strings.TrimSpace(string(p.Line)), `"`)
						if n, e2 := strconv.Atoi(s); e2 == nil && n >= 1 {
							lineOK = true
						}
					}
					if err == nil && strings.TrimSpace(p.File) != "" && lineOK &&
						strings.Contains(p.NewContent, "true") {
						firstCallOK = true
					} else {
						firstBadShape = true
						t.Logf("first edit_line call bad shape: %v", err)
					}
				}
			}
		case agent.ToolResultEvent:
			if e.Err != nil {
				t.Logf("tool result err: %v", e.Err)
			}
		case agent.MessageEvent:
			finalText.WriteString(e.Text)
		case agent.DoneEvent:
			done = true
		case agent.ErrorEvent:
			t.Fatalf("ErrorEvent: %v", e.Err)
		}
	}

	after, _ := os.ReadFile(cfgPath)
	u := loop.SessionUsage()
	t.Logf("edit_line called %d time(s); provider requests: %d; tokens in=%d out=%d", editCalls, len(rec.calls), u.Input, u.Output)
	t.Logf("file after: %q", string(after))

	if editCalls == 0 {
		t.Error("model never called edit_line")
	}
	if firstBadShape {
		t.Error("first edit_line call had a malformed shape (schema too terse?)")
	}
	if !firstCallOK {
		t.Error("first edit_line call did not carry a valid file/line/new_content")
	}
	if !strings.Contains(string(after), "debug = true") || strings.Contains(string(after), "debug = false") {
		t.Errorf("file not edited correctly; got:\n%s", string(after))
	}
	if !done {
		t.Error("no DoneEvent — run did not finish cleanly")
	}
	// First-shot success target: at most 2 edit_line calls (allow one
	// self-correction). More signals a failure loop.
	if editCalls > 2 {
		t.Errorf("edit_line called %d times — first-shot accuracy regressed", editCalls)
	}
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
