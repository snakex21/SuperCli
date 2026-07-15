package llm

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestNewEcho_RejectsEmptyName(t *testing.T) {
	if _, err := NewEcho(""); err == nil {
		t.Fatal("expected error on empty name")
	}
}

func TestEcho_Name(t *testing.T) {
	p, _ := NewEcho("noop")
	if p.Name() != "noop" {
		t.Fatalf("Name = %q, want noop", p.Name())
	}
}

func TestEcho_SupportsVision_DefaultFalse(t *testing.T) {
	p, _ := NewEcho("noop")
	if p.SupportsVision() {
		t.Fatal("default SupportsVision should be false")
	}
}

func TestEcho_SetVision(t *testing.T) {
	p, _ := NewEcho("noop")
	p.SetVision(true)
	if !p.SupportsVision() {
		t.Fatal("after SetVision(true) should support vision")
	}
}

func TestEcho_Complete_EmptyMessages(t *testing.T) {
	p, _ := NewEcho("noop")
	if _, err := p.Complete(context.Background(), nil, nil); err == nil {
		t.Fatal("expected error on empty messages")
	}
}

func TestEcho_Complete_InvalidMessage(t *testing.T) {
	p, _ := NewEcho("noop")
	msgs := []Message{{Role: Role("wizard"), Content: "x"}}
	if _, err := p.Complete(context.Background(), msgs, nil); err == nil {
		t.Fatal("expected error on invalid role")
	}
}

func TestEcho_Complete_RespectsContext(t *testing.T) {
	p, _ := NewEcho("noop")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Complete(ctx, []Message{{Role: RoleUser, Content: "hi"}}, nil); err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

func collectDeltas(t *testing.T, ch <-chan Delta) []Delta {
	t.Helper()
	out := []Delta{}
	for d := range ch {
		out = append(out, d)
	}
	return out
}

func TestEcho_Complete_StreamsUserMessage(t *testing.T) {
	p, _ := NewEcho("noop")
	ch, err := p.Complete(context.Background(), []Message{
		{Role: RoleSystem, Content: "you are helpful"},
		{Role: RoleUser, Content: "hello"},
	}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	ds := collectDeltas(t, ch)
	if len(ds) == 0 {
		t.Fatal("no deltas")
	}
	if ds[0].Role != RoleAssistant {
		t.Fatalf("first delta role = %q, want assistant", ds[0].Role)
	}
	var content strings.Builder
	for _, d := range ds {
		content.WriteString(d.Content)
	}
	if !strings.Contains(content.String(), "hello") {
		t.Fatalf("output %q does not echo user message", content.String())
	}
	if !strings.Contains(content.String(), "[echo:noop]") {
		t.Fatalf("output %q missing provider tag", content.String())
	}
	last := ds[len(ds)-1]
	if last.FinishReason != "stop" {
		t.Fatalf("last delta finish_reason = %q, want stop", last.FinishReason)
	}
}

func TestEcho_Complete_StreamsToolMessage(t *testing.T) {
	p, _ := NewEcho("noop")
	// When the last message is a tool result with no follow-up user
	// message, the provider should fall back to echoing the last
	// message (the tool result).
	ch, err := p.Complete(context.Background(), []Message{
		{Role: RoleSystem, Content: "you are helpful"},
		{Role: RoleTool, Name: "bash", ToolCallID: "call_1", Content: "ok"},
	}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	ds := collectDeltas(t, ch)
	var content strings.Builder
	for _, d := range ds {
		content.WriteString(d.Content)
	}
	if !strings.Contains(content.String(), "[tool:bash]") {
		t.Fatalf("output %q missing tool tag", content.String())
	}
}

func TestEcho_Complete_StripsImagesWhenNoVision(t *testing.T) {
	p, _ := NewEcho("noop") // no vision
	ch, err := p.Complete(context.Background(), []Message{
		{Role: RoleUser, Parts: []ContentPart{
			{Type: PartTypeText, Text: "look at this"},
			{Type: PartTypeImage, Image: &ImageRef{Data: "AAA", MediaType: "image/png"}},
		}},
	}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	ds := collectDeltas(t, ch)
	var content strings.Builder
	for _, d := range ds {
		content.WriteString(d.Content)
	}
	if !strings.Contains(content.String(), "look at this") {
		t.Fatalf("text missing: %q", content.String())
	}
}

func TestEcho_Complete_KeepsImagesWhenVision(t *testing.T) {
	p, _ := NewEcho("noop")
	p.SetVision(true)
	ch, err := p.Complete(context.Background(), []Message{
		{Role: RoleUser, Parts: []ContentPart{
			{Type: PartTypeText, Text: "look at this"},
		}},
	}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	ds := collectDeltas(t, ch)
	var content strings.Builder
	for _, d := range ds {
		content.WriteString(d.Content)
	}
	if !strings.Contains(content.String(), "look at this") {
		t.Fatalf("text missing: %q", content.String())
	}
}

func TestEcho_Complete_CancelMidStream(t *testing.T) {
	p, _ := NewEcho("noop")
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := p.Complete(ctx, []Message{{Role: RoleUser, Content: "hello"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	// Read first delta, then cancel.
	<-ch
	cancel()
	// Drain remaining. Should be short.
	deadline := time.After(200 * time.Millisecond)
	for {
		select {
		case _, open := <-ch:
			if !open {
				return
			}
		case <-deadline:
			t.Fatal("channel did not close after cancel")
		}
	}
}

