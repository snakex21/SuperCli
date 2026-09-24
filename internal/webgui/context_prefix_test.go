package webgui

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/memory"
)

type prefixCaptureProvider struct {
	requests [][]llm.Message
	tools    [][]llm.ToolDef
}

func (p *prefixCaptureProvider) Name() string         { return "echo-test" }
func (p *prefixCaptureProvider) SupportsVision() bool { return false }
func (p *prefixCaptureProvider) Complete(_ context.Context, msgs []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	p.requests = append(p.requests, append([]llm.Message(nil), msgs...))
	p.tools = append(p.tools, append([]llm.ToolDef(nil), defs...))
	ch := make(chan llm.Delta, 1)
	ch <- llm.Delta{Content: "Ready.", FinishReason: "stop"}
	close(ch)
	return ch, nil
}

func TestWebMemoryUpdatePreservesConversationPrefix(t *testing.T) {
	eng, err := NewEngine(echoConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	p := &prefixCaptureProvider{}
	eng.prov = p
	global, _ := eng.webMemoryStores(eng.Home())
	if global == nil {
		t.Fatal("memory unavailable")
	}
	save := func(value string) {
		t.Helper()
		if err := global.Put(memory.Entry{ID: "prefix-test", Scope: memory.ScopePreference, Content: value, Source: memory.SourceAgent}); err != nil {
			t.Fatal(err)
		}
	}
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "Inspect this repository and remember the result."},
		{Role: llm.RoleAssistant, Content: strings.Repeat("Recorded code findings. ", 1800)},
	}
	request := func() {
		t.Helper()
		loop, err := eng.newLoopWithSession(history, nil)
		if err != nil {
			t.Fatal(err)
		}
		ch, err := loop.Run(context.Background(), "Update the source code in main.go.")
		if err != nil {
			t.Fatal(err)
		}
		for ev := range ch {
			if e, ok := ev.(agent.ErrorEvent); ok {
				t.Fatal(e.Err)
			}
		}
	}
	save("Output language: Polish.")
	request()
	save("Output language: English.")
	request()
	if len(p.requests) != 2 {
		t.Fatalf("model calls=%d, want exactly one per turn", len(p.requests))
	}
	first, second := p.requests[0], p.requests[1]
	if !reflect.DeepEqual(p.tools[0], p.tools[1]) {
		t.Fatal("memory update changed tool definitions")
	}
	if len(first) != len(second) || !reflect.DeepEqual(first[:len(first)-1], second[:len(second)-1]) {
		t.Fatal("changing remembered preference rewrote the prefix before the conversation")
	}
	all := func(msgs []llm.Message) string {
		var b strings.Builder
		for _, m := range msgs {
			b.WriteString(m.TextOnly().Content)
		}
		return b.String()
	}
	if strings.Count(all(first), "Output language: Polish.") != 1 || strings.Count(all(second), "Output language: English.") != 1 {
		t.Fatal("current memory missing or duplicated")
	}
	if strings.Contains(all(second), "Output language: Polish.") {
		t.Fatal("stale preference retained")
	}
	if !strings.Contains(second[len(second)-1].Content, "Output language: English.") {
		t.Fatal("latest memory missing from request tail")
	}
	t.Logf("stable conversation prefix: %d estimated tokens", llm.EstimateTokens(second[:len(second)-1]))
}
