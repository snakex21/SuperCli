package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/llm"
)

func TestReasoningMenuChangesNextLocalRequest(t *testing.T) {
	t.Cleanup(func() { _ = llm.SetReasoningEffort("") })
	captured := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Effort string `json:"reasoning_effort"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		captured <- req.Effort
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "qwen", Reasoning: true, ReasoningToggleOnly: true})
	p, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: srv.URL, Model: "qwen", Capabilities: caps})
	if err != nil {
		t.Fatal(err)
	}
	m := New(Options{Home: t.TempDir(), DataDir: t.TempDir(), LLM: p})
	for _, level := range []string{"high", "none", "high", ""} {
		out, _ := m.openReasoningMenu()
		m = out.(Model)
		m.menu.cursor = m.reasoningOptionIndex(level)
		out, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = out.(Model)
		ch, err := p.Complete(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "OK"}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		for d := range ch {
			if d.Err != nil {
				t.Fatal(d.Err)
			}
		}
		if got := <-captured; got != level {
			t.Fatalf("menu=%q wire=%q", level, got)
		}
	}
	out, _ := m.openReasoningMenu()
	if view := out.(Model).renderReasoningMenu(); !strings.Contains(view, "on/off only") {
		t.Fatalf("toggle-only explanation missing: %s", view)
	}
}
