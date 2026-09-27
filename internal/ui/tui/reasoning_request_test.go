package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/llm"
	"supercli/internal/system/config"
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

func TestReasoningMenuMaxSavesAndReachesProvider(t *testing.T) {
	t.Cleanup(func() { _ = llm.SetReasoningEffort("") })
	captured := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Effort string `json:"reasoning_effort"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		captured <- req.Effort
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"OK"},"finish_reason":"stop"}]}

data: [DONE]

`)
	}))
	defer srv.Close()
	for _, language := range []string{"pl", "en"} {
		t.Run(language, func(t *testing.T) {
			p, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: srv.URL, Model: "gpt-6-astra"})
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			m := New(Options{Home: dir, DataDir: dir, Language: language, LLM: p})
			m.input.SetValue("draft")
			out, _ := m.openReasoningMenu()
			m = out.(Model)
			index := m.reasoningOptionIndex("max")
			if index == 0 {
				t.Fatal("max missing from reasoning menu")
			}
			m.menu.cursor = index
			out, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = out.(Model)
			if m.input.Value() != "draft" || !strings.Contains(m.renderHeader(), "max") {
				t.Fatal("selection lost draft or did not refresh header")
			}
			saved, err := config.LoadToml(filepath.Join(dir, "config.toml"))
			if err != nil || saved.ReasoningEffort != "max" {
				t.Fatalf("saved max=%q, err=%v", saved.ReasoningEffort, err)
			}
			ch, err := p.Complete(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "OK"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			for d := range ch {
				if d.Err != nil {
					t.Fatal(d.Err)
				}
			}
			if got := <-captured; got != "max" {
				t.Fatalf("menu max sent %q", got)
			}
		})
	}
}

func TestReasoningMenuMarksFallbackFromMaxActive(t *testing.T) {
	const model = "reasoning-max-menu-fixture"
	t.Cleanup(func() { _ = llm.SetReasoningEffort("") })
	llm.SetReasoningEffortSupport(model, []string{"low", "high", "xhigh"})
	_ = llm.SetReasoningEffort("max")
	p, _ := newStubLLM(model)
	m := New(Options{Home: t.TempDir(), DataDir: t.TempDir(), LLM: p, NoColor: true})
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	out, _ = out.(Model).openReasoningMenu()
	m = out.(Model)
	if m.localizedReasoningMenuOptions()[m.menu.cursor].Value != "xhigh" {
		t.Fatal("fallback not selected")
	}
	view := m.renderReasoningMenu()
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "xhigh") && strings.Contains(line, "active") {
			return
		}
	}
	t.Fatalf("effective fallback not marked active: %s", view)
}
