package agent

import (
	"context"
	"supercli/internal/llm"
	"testing"
	"time"
)

func TestConsumeMetadataDoesNotTrainPrefill(t *testing.T) {
	l := &Loop{modelID: "m", contextProvider: "test", prefillProfiles: llm.LoadPrefillProfiles(t.TempDir())}
	stream := make(chan llm.Delta)
	go func() {
		time.Sleep(20 * time.Millisecond)
		stream <- llm.Delta{Role: llm.RoleAssistant}
		stream <- llm.Delta{Usage: &llm.Usage{Input: 12000}}
		stream <- llm.Delta{FinishReason: "stop"}
		close(stream)
	}()
	text, calls, usage, err := l.consume(context.Background(), stream, make(chan Event, 4))
	if err != nil || text != "" || len(calls) != 0 || usage.Input != 12000 {
		t.Fatalf("consume changed: text=%q calls=%v usage=%v err=%v", text, calls, usage, err)
	}
	l.observePrefillCall(12000, usage, l.lastCallTTFT)
	if l.lastCallTTFT != 0 {
		t.Errorf("metadata ended backend wait: %v", l.lastCallTTFT)
	}
	if profile, ok := l.PrefillProfile(); ok {
		t.Errorf("metadata trained context profile: %+v", profile)
	}
}

func TestConsumeWaitsForGeneratedOutputAfterRole(t *testing.T) {
	for _, generated := range []llm.Delta{{Content: "answer"}, {Reasoning: "analysis"}, {OutputStarted: true}} {
		l := &Loop{}
		input := make(chan llm.Delta)
		output := make(chan Event)
		done := make(chan struct{})
		go func() { defer close(done); _, _, _, _ = l.consume(context.Background(), input, output) }()
		input <- llm.Delta{Role: llm.RoleAssistant}
		input <- llm.Delta{Usage: &llm.Usage{Input: 12000}}
		input <- llm.Delta{Notice: "barrier"}
		<-output // synchronizes after consuming metadata; no generated output yet
		if l.lastCallTTFT != 0 {
			t.Errorf("role/usage started TTFT: %v", l.lastCallTTFT)
		}
		time.Sleep(20 * time.Millisecond) // ensure measurable elapsed time on Windows
		input <- generated
		if generated.Content != "" || generated.Reasoning != "" {
			<-output
		}
		close(input)
		<-done
		if l.lastCallTTFT < 15*time.Millisecond {
			t.Errorf("missing generation delay: %v", l.lastCallTTFT)
		}
	}
}

func TestConsumeProgressDoesNotEnterHistoryOrExecuteTool(t *testing.T) {
	l := &Loop{}
	stream := make(chan llm.Delta, 2)
	stream <- llm.Delta{OutputStarted: true}
	stream <- llm.Delta{FinishReason: "stop"}
	close(stream)
	output := make(chan Event, 4)
	text, calls, _, err := l.consume(context.Background(), stream, output)
	if err != nil || text != "" || len(calls) != 0 || len(output) != 0 {
		t.Fatalf("progress leaked: text=%q calls=%v events=%d err=%v", text, calls, len(output), err)
	}
}
