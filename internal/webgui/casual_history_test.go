package webgui

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
)

type casualHistoryProvider struct{ prefixCaptureProvider }

func (p *casualHistoryProvider) Complete(ctx context.Context, msgs []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	p.requests = append(p.requests, append([]llm.Message(nil), msgs...))
	p.tools = append(p.tools, append([]llm.ToolDef(nil), defs...))
	ch := make(chan llm.Delta, 2)
	ch <- llm.Delta{Reasoning: "archived-reasoning-marker"}
	ch <- llm.Delta{Content: "visible-reply-marker", FinishReason: "stop"}
	close(ch)
	return ch, nil
}

func TestCasualHistoryStaysLightAcrossWebRequests(t *testing.T) {
	t.Setenv("SUPERCLI_KEEP_THINKING", "")
	ctx := context.Background()
	home, data := t.TempDir(), t.TempDir()
	eng, err := NewEngine(echoConfig(), home, data)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()
	p := &casualHistoryProvider{}
	eng.prov = p
	var sid string
	var notices []string
	var reasoning strings.Builder
	emit := func(ev wireEvent) {
		if ev.Type == "session" {
			sid = ev.SessionID
		}
		if ev.Type == "reasoning" {
			reasoning.WriteString(ev.Text)
		}
		if ev.Type == "notice" {
			notices = append(notices, ev.Text)
		}
	}
	if err := eng.runStream(ctx, "cześć", "", "", emit); err != nil {
		t.Fatal(err)
	}
	if sid == "" {
		t.Fatal("missing session id")
	}
	// A real GUI continuation reconstructs the Loop from persisted messages.
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	eng, err = NewEngine(echoConfig(), home, data)
	if err != nil {
		t.Fatal(err)
	}
	eng.prov = p
	if err := eng.runStream(ctx, "narazie nie wiem właśnie zastanawiam się", sid, "", emit); err != nil {
		t.Fatal(err)
	}
	if len(p.requests) != 2 {
		t.Fatalf("calls=%d, want one per turn", len(p.requests))
	}
	if !reflect.DeepEqual(p.tools[0], p.tools[1]) || !reflect.DeepEqual(p.requests[0][0], p.requests[1][0]) {
		t.Error("casual continuation loaded coordinator tools or changed stable prompt")
	}
	var request strings.Builder
	for _, m := range p.requests[1] {
		request.WriteString(m.TextOnly().Content)
	}
	if strings.Contains(request.String(), "archived-reasoning-marker") {
		t.Error("GUI reloaded reasoning into model context")
	}
	if !strings.Contains(request.String(), "visible-reply-marker") {
		t.Error("previous visible answer lost")
	}
	for _, notice := range notices {
		if strings.Contains(notice, "preflight") {
			t.Error("casual conversation loaded repo preflight")
		}
	}
	if reasoning.String() != strings.Repeat("archived-reasoning-marker", 2) {
		t.Error("UI reasoning stream lost")
	}
	store, err := eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.ReadMessages(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	var archived strings.Builder
	for _, row := range rows {
		m, err := row.ToMessage()
		if err != nil {
			t.Fatal(err)
		}
		archived.WriteString(m.TextOnly().Content)
	}
	if strings.Count(archived.String(), "archived-reasoning-marker") != 2 {
		t.Error("persisted reasoning lost")
	}
	t.Logf("message-token estimates: first=%d second=%d", llm.EstimateTokens(p.requests[0]), llm.EstimateTokens(p.requests[1]))
	notices = nil
	if err := eng.runStream(ctx, "sprawdź pliki w repo", sid, "", emit); err != nil {
		t.Fatal(err)
	}
	if len(p.requests) != 3 {
		t.Fatalf("calls=%d, want 3", len(p.requests))
	}
	if reflect.DeepEqual(p.tools[1], p.tools[2]) {
		t.Error("project request did not restore full tools")
	}
	found := false
	for _, notice := range notices {
		found = found || strings.Contains(notice, "preflight")
	}
	if !found {
		t.Error("first actual project request lost deferred preflight")
	}
}
