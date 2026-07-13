package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestLoopInvoke_LargeSuccessUsesRetrievablePreview(t *testing.T) {
	const full = "FULL_OUTPUT_END"
	large := "FULL_OUTPUT_BEGIN\n" + strings.Repeat("abcdefghij", 2000) + "\n" + full
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name:        "large_read",
		Description: "large read",
		ReadOnly:    true,
		Schema:      `{}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: large}, nil
		},
	})
	loop, err := NewLoop(LoopConfig{Provider: echoProvider("stub"), Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 4)
	result := loop.invoke(context.Background(), llm.ToolCall{ID: "c1", Name: "large_read", Arguments: `{}`}, events)
	if result.failed || len(result.followUps) != 1 {
		t.Fatalf("invoke result = %+v", result)
	}
	preview := result.followUps[0].Content
	if len(preview) >= len(large)/2 || !strings.Contains(preview, "handle=out_000001") {
		t.Fatalf("large result not compacted: preview=%d full=%d\n%s", len(preview), len(large), preview)
	}
	if !strings.Contains(preview, "FULL_OUTPUT_BEGIN") || !strings.Contains(preview, full) {
		t.Fatal("preview must retain useful head and tail")
	}

	read, ok := reg.Get("read_output")
	if !ok {
		t.Fatal("read_output not registered")
	}
	chunk, callErr := read.Fn(context.Background(), json.RawMessage(`{"handle":"out_000001","offset":0,"limit":8192}`))
	if callErr != nil || chunk.Err != nil || !strings.Contains(chunk.Text, "FULL_OUTPUT_BEGIN") {
		t.Fatalf("stored output not retrievable: result=%+v err=%v", chunk, callErr)
	}

	// UI events retain the full text; only the provider-facing history is
	// compacted. This keeps diagnostics and expansion views lossless.
	close(events)
	foundFull := false
	for event := range events {
		if ev, ok := event.(ToolResultEvent); ok && ev.Output == large {
			foundFull = true
		}
	}
	if !foundFull {
		t.Fatal("ToolResultEvent did not retain the complete output")
	}
}
