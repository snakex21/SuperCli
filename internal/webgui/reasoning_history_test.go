package webgui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

type nativeHistoryProvider struct{ prefixCaptureProvider }

func (p *nativeHistoryProvider) Complete(_ context.Context, msgs []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	p.requests = append(p.requests, append([]llm.Message(nil), msgs...))
	p.tools = append(p.tools, append([]llm.ToolDef(nil), defs...))
	ch := make(chan llm.Delta, 4)
	ch <- llm.Delta{Reasoning: "display-only-marker"}
	ch <- llm.Delta{NativeReasoning: &llm.ReasoningBlock{Format: llm.ReasoningResponses, Model: "fixture",
		Scope: "fixture-endpoint", Data: json.RawMessage("{\"type\":\"reasoning\",\"encrypted_content\":\"opaque-marker\",\"summary\":[]}")}}
	ch <- llm.Delta{Content: "Visible answer."}
	ch <- llm.Delta{FinishReason: "stop", Usage: &llm.Usage{Input: 20, Output: 15, Reasoning: 8}}
	close(ch)
	return ch, nil
}

func TestNativeReasoningSurvivesGUIRestart(t *testing.T) {
	for _, drop := range []bool{false, true} {
		name := "keep"
		if drop {
			name = "drop"
		}
		t.Run(name, func(t *testing.T) { testNativeReasoningGUIRestart(t, drop) })
	}
}

func testNativeReasoningGUIRestart(t *testing.T, drop bool) {
	old := llm.DiscardPreviousReasoning()
	t.Cleanup(func() { llm.SetDiscardPreviousReasoning(old) })
	t.Setenv("SUPERCLI_KEEP_THINKING", "")
	ctx := context.Background()
	home, data := t.TempDir(), t.TempDir()
	eng, err := NewEngine(echoConfig(), home, data)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()
	value := "off"
	if drop {
		value = "on"
	}
	srv := &Server{eng: eng}
	if rec := knobsPOST(t, srv, `{"key":"discard_previous_reasoning","value":"`+value+`"}`); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if k := findKnob(t, knobsGET(t, srv), "discard_previous_reasoning"); k.Default != "off" || k.State != value {
		t.Fatalf("setting=%+v", k)
	}
	p := &nativeHistoryProvider{}
	eng.prov = p
	var sid string
	var displayed strings.Builder
	emit := func(ev wireEvent) {
		if ev.Type == "session" {
			sid = ev.SessionID
		}
		if ev.Type == "reasoning" {
			displayed.WriteString(ev.Text)
		}
	}
	if err := eng.runStream(ctx, "cześć", "", "", emit); err != nil {
		t.Fatal(err)
	}
	if sid == "" {
		t.Fatal("session missing")
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	// Mirror binary startup, including loading the persisted preference.
	llm.SetDiscardPreviousReasoning(false)
	global, _ := config.FindTomlPaths(data, home)
	cfg, err := config.LoadToml(global)
	if err != nil {
		t.Fatal(err)
	}
	config.ApplyLLMGlobals(cfg, nil)
	eng, err = NewEngine(echoConfig(), home, data)
	if err != nil {
		t.Fatal(err)
	}
	eng.prov = p
	if err := eng.runStream(ctx, "narazie nie wiem właśnie zastanawiam się", sid, "", emit); err != nil {
		t.Fatal(err)
	}
	if len(p.requests) != 2 {
		t.Fatalf("requests=%d", len(p.requests))
	}
	blocks := 0
	for _, m := range p.requests[1] {
		if strings.Contains(m.TextOnly().Content, "display-only-marker") || strings.Contains(m.TextOnly().Content, "opaque-marker") {
			t.Fatal("native/display reasoning leaked into instructions")
		}
		for _, part := range m.Parts {
			if part.Type == llm.PartTypeReasoning {
				blocks++
				if part.Reasoning.Tokens != 8 || !strings.Contains(string(part.Reasoning.Data), "opaque-marker") {
					t.Fatal("native payload or token count lost on restart")
				}
			}
		}
	}
	wantBlocks := 1
	if drop {
		wantBlocks = 0
	}
	if blocks != wantBlocks {
		t.Fatalf("native blocks=%d", blocks)
	}
	if displayed.String() != strings.Repeat("display-only-marker", 2) {
		t.Fatal("display reasoning changed")
	}
	store, err := eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.ReadMessages(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	archived := 0
	for _, row := range rows {
		m, err := row.ToMessage()
		if err != nil {
			t.Fatal(err)
		}
		if m.HasNativeReasoning() && strings.Contains(m.TextOnly().Content, "display-only-marker") {
			archived++
		}
	}
	if archived != 2 {
		t.Fatalf("archived display/native pairs=%d", archived)
	}
}
