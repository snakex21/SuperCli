//go:build integration

package test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/tools"
)

// sequenceProvider returns tool calls in a predefined order,
// simulating a model that follows a plan step-by-step.
type sequenceProvider struct {
	steps []providerStep
	step  int
}

type providerStep struct {
	content     string       // optional text to emit before tool calls
	toolCalls   []llm.ToolCall // tool calls to emit
	finishAfter bool         // true = emit DoneEvent after tools
}

func (p *sequenceProvider) Name() string        { return "seq-provider" }
func (p *sequenceProvider) SupportsVision() bool { return false }

func (p *sequenceProvider) Complete(ctx context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	ch := make(chan llm.Delta, 16)
	go func() {
		defer close(ch)
		if p.step >= len(p.steps) {
			// No more steps — finish.
			ch <- llm.Delta{FinishReason: "stop"}
			return
		}
		s := p.steps[p.step]
		p.step++

		// Emit text content first.
		if s.content != "" {
			ch <- llm.Delta{Content: s.content}
		}

		// Emit tool calls — ONE delta per call with all fields set.
		for i := range s.toolCalls {
			tc := s.toolCalls[i]
			ch <- llm.Delta{
				ToolCall: &llm.ToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments},
			}
		}

		// Finish.
		reason := "tool_calls"
		if s.finishAfter {
			reason = "stop"
		}
		ch <- llm.Delta{FinishReason: reason}
	}()
	return ch, nil
}

// TestIntegration_ToolCallPipeline tests the full file operation
// pipeline: create → read → edit → verify → delete.
func TestIntegration_ToolCallPipeline(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "lubie_fifticale.txt")

	// Step 0: create the initial file (simulating write_file tool).
	if err := os.WriteFile(filePath, []byte("lubie fifticale\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Register tools.
	reg := tools.NewRegistry()
	readLines := tools.NewReadLines(dir)
	editLine := tools.NewEditLine(dir)
	deleteLines := tools.NewDeleteLines(dir)
	reg.MustRegister(readLines.Spec())
	reg.MustRegister(editLine.Spec())
	reg.MustRegister(deleteLines.Spec())

	// Build step sequence: read → edit → verify → delete.
	prov := &sequenceProvider{
		steps: []providerStep{
			// Step 1: read the file to verify initial content.
			{
				content: "Let me read the file to check its contents.",
				toolCalls: []llm.ToolCall{
					{ID: "call_1", Name: "read_lines", Arguments: `{"file":"lubie_fifticale.txt","from":1,"to":1}`},
				},
			},
			// Step 2: edit the file — change content.
			{
				content: "Now I'll change the text.",
				toolCalls: []llm.ToolCall{
					{ID: "call_2", Name: "edit_line", Arguments: `{"file":"lubie_fifticale.txt","line":1,"new_content":"windows is better than linux"}`},
				},
			},
			// Step 3: read again to verify the edit.
			{
				content: "Let me verify the change took effect.",
				toolCalls: []llm.ToolCall{
					{ID: "call_3", Name: "read_lines", Arguments: `{"file":"lubie_fifticale.txt","from":1,"to":1}`},
				},
				finishAfter: true,
			},
		},
	}

	loop, err := agent.NewLoop(agent.LoopConfig{
		Provider: prov,
		Registry: reg,
		MaxSteps: 5,
		System:   "You are a test agent.",
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Run: "verify file lubie_fifticale.txt, change content, and verify again"
	ch, err := loop.Run(ctx, "Edit lubie_fifticale.txt to say 'windows is better than linux'")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var toolResults []string
	for ev := range ch {
		switch e := ev.(type) {
		case agent.MessageEvent:
			t.Logf("msg: %s", e.Text)
		case agent.ToolCallEvent:
			t.Logf("tool call: %s(%s)", e.Name, e.Args)
		case agent.ToolResultEvent:
			if e.Err != nil {
				t.Errorf("tool error: %s: %v", e.ID, e.Err)
			}
			toolResults = append(toolResults, e.Output)
			t.Logf("tool result: %s → %q", e.ID, truncate(e.Output, 80))
		case agent.ErrorEvent:
			t.Fatalf("agent error: %v", e.Err)
		}
	}

	// Verify step 1: read returned original content.
	if len(toolResults) < 1 || !strings.Contains(toolResults[0], "lubie fifticale") {
		t.Fatalf("step 1 read: expected 'lubie fifticale' in result, got: %v", toolResults)
	}

	// Verify step 2: edit succeeded (no error).
	// Already checked by ToolResultEvent handler above.

	// Verify step 3: read returned modified content.
	if len(toolResults) < 2 || !strings.Contains(toolResults[1], "windows is better than linux") {
		t.Fatalf("step 3 verify: expected 'windows is better than linux' in result, got: %v", toolResults)
	}

	// Verify file on disk matches.
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("final read: %v", err)
	}
	content := strings.TrimSpace(string(data))
	if content != "windows is better than linux" {
		t.Errorf("file content = %q, want 'windows is better than linux'", content)
	}

	// Step 4: delete the file.
	if err := os.Remove(filePath); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("file should not exist after deletion")
	}

	t.Logf("pipeline: create → read → edit → verify → delete ✓")
}

// TestIntegration_ToolCallPipeline_WithVerification adds F4-style
// verification: after each tool call, we check the actual file
// state matches expectations.
func TestIntegration_ToolCallPipeline_WithVerification(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "lubie_fifticale.txt")

	// Create initial file.
	if err := os.WriteFile(filePath, []byte("lubie fifticale\n"), 0644); err != nil {
		t.Fatal(err)
	}

	reg := tools.NewRegistry()
	reg.MustRegister(tools.NewReadLines(dir).Spec())
	reg.MustRegister(tools.NewEditLine(dir).Spec())

	prov := &sequenceProvider{
		steps: []providerStep{
			{
				content: "Reading the file.",
				toolCalls: []llm.ToolCall{
					{ID: "c1", Name: "read_lines", Arguments: `{"file":"lubie_fifticale.txt","from":1,"to":1}`},
				},
			},
			{
				content: "Editing.",
				toolCalls: []llm.ToolCall{
					{ID: "c2", Name: "edit_line", Arguments: `{"file":"lubie_fifticale.txt","line":1,"new_content":"windows is better than linux"}`},
				},
				finishAfter: true,
			},
		},
	}

	loop, _ := agent.NewLoop(agent.LoopConfig{
		Provider: prov,
		Registry: reg,
		MaxSteps: 3,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, _ := loop.Run(ctx, "change lubie_fifticale.txt")
	for ev := range ch {
		if e, ok := ev.(agent.ErrorEvent); ok {
			t.Fatalf("error: %v", e.Err)
		}
	}

	// F4-style verification: read the file directly.
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("verification read: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "windows is better than linux"
	if got != want {
		t.Errorf("F4 verification failed: file = %q, want %q", got, want)
	}

	// Cleanup.
	os.Remove(filePath)
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("cleanup: file should not exist")
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// TestIntegration_ToolCallPipeline_DeleteLines tests the
// delete_lines tool as part of the pipeline.
func TestIntegration_ToolCallPipeline_DeleteLines(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "multi.txt")
	os.WriteFile(filePath, []byte("line 1\nline 2\nline 3\n"), 0644)

	reg := tools.NewRegistry()
	reg.MustRegister(tools.NewReadLines(dir).Spec())
	reg.MustRegister(tools.NewDeleteLines(dir).Spec())

	prov := &sequenceProvider{
		steps: []providerStep{
			{
				content: "I'll delete line 2.",
				toolCalls: []llm.ToolCall{
					{ID: "d1", Name: "delete_lines", Arguments: `{"file":"multi.txt","from":2,"to":2}`},
				},
				finishAfter: true,
			},
		},
	}

	loop, _ := agent.NewLoop(agent.LoopConfig{
		Provider: prov,
		Registry: reg,
		MaxSteps: 2,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ch, _ := loop.Run(ctx, "delete line 2 from multi.txt")
	for ev := range ch {
		if e, ok := ev.(agent.ErrorEvent); ok {
			t.Fatalf("error: %v", e.Err)
		}
	}

	// Verify: file should have lines 1 and 3 only.
	data, _ := os.ReadFile(filePath)
	got := strings.TrimSpace(string(data))
	if got != "line 1\nline 3" {
		t.Errorf("after delete: %q, want 'line 1\\nline 3'", got)
	}
	os.Remove(filePath)
}

// TestIntegration_ToolCallPipeline_ErrorHandling tests that
// tool errors are propagated correctly.
func TestIntegration_ToolCallPipeline_ErrorHandling(t *testing.T) {
	dir := t.TempDir()

	reg := tools.NewRegistry()
	reg.MustRegister(tools.NewReadLines(dir).Spec())

	prov := &sequenceProvider{
		steps: []providerStep{
			{
				content: "Reading a nonexistent file.",
				toolCalls: []llm.ToolCall{
					{ID: "e1", Name: "read_lines", Arguments: `{"file":"nonexistent.txt","from":1,"to":1}`},
				},
				finishAfter: true,
			},
		},
	}

	loop, _ := agent.NewLoop(agent.LoopConfig{
		Provider: prov,
		Registry: reg,
		MaxSteps: 2,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ch, _ := loop.Run(ctx, "read nonexistent.txt")
	hasError := false
	for ev := range ch {
		switch e := ev.(type) {
		case agent.ToolResultEvent:
			if e.Err != nil {
				hasError = true
				t.Logf("expected tool error: %v", e.Err)
			}
		case agent.ErrorEvent:
			t.Logf("agent error (acceptable): %v", e.Err)
		}
	}
	if !hasError {
		t.Error("expected tool error for nonexistent file")
	}
}

func init() {
	// Ensure JSON encoding works for arguments.
	_ = json.Marshal
	_ = fmt.Sprintf
}
