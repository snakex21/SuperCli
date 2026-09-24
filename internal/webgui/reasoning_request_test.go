package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestReasoningAPIChangesNextProviderRequest(t *testing.T) {
	t.Cleanup(func() { _ = llm.SetReasoningEffort("") })
	srv := newTestServer(t, false)
	captured := make(chan string, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Reasoning struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		captured <- req.Reasoning.Effort
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{}}\n\n")
	}))
	defer upstream.Close()
	p, err := llm.NewResponses(llm.ResponsesConfig{BaseURL: upstream.URL, Model: "custom-thinker"})
	if err != nil {
		t.Fatal(err)
	}
	srv.eng.prov = p
	for _, level := range []string{"low", "high", "none", "default"} {
		rec := httptest.NewRecorder()
		srv.handleReasoning(rec, httptest.NewRequest(http.MethodPost, "/api/reasoning", strings.NewReader(`{"level":"`+level+`"}`)))
		if rec.Code != 200 {
			t.Fatal(rec.Body.String())
		}
		var view reasoningView
		if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
			t.Fatal(err)
		}
		want := level
		if want == "default" {
			want = ""
		}
		if view.Effective != want {
			t.Fatalf("view=%+v want=%q", view, want)
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
		if got := <-captured; got != want {
			t.Fatalf("API level=%q sent=%q", level, got)
		}
	}
}

func TestReasoningViewShowsLocalToggleOnly(t *testing.T) {
	t.Cleanup(func() { _ = llm.SetReasoningEffort("") })
	srv := newTestServer(t, false)
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "qwen", Reasoning: true, ReasoningToggleOnly: true})
	p, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: "http://localhost:1234/v1", Model: "qwen", Capabilities: caps})
	if err != nil {
		t.Fatal(err)
	}
	srv.eng.prov = p
	_ = llm.SetReasoningEffort("low")
	view := srv.reasoningView(p.Name())
	if !view.ToggleOnly || view.Effective != "on" || view.Configured != "low" {
		t.Fatalf("view=%+v", view)
	}
}

func TestReasoningSelectionPersistsBeforeAnotherMessage(t *testing.T) {
	t.Cleanup(func() { _ = llm.SetReasoningEffort("") })
	srv := newTestServer(t, false)
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(srv.eng.Home(), "old-model", "selection")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetRuntime(sess.ID, "old-provider", "old-model", "low"); err != nil {
		t.Fatal(err)
	}
	for _, level := range []string{"high", "default"} {
		rec := httptest.NewRecorder()
		body := fmt.Sprintf(`{"session_id":%q,"level":%q}`, sess.ID, level)
		srv.handleReasoning(rec, httptest.NewRequest(http.MethodPost, "/api/reasoning", strings.NewReader(body)))
		if rec.Code != 200 {
			t.Fatal(rec.Body.String())
		}
		var response reasoningResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		got, err := store.Get(sess.ID)
		if err != nil {
			t.Fatal(err)
		}
		want := level
		if level == "default" {
			want = ""
		}
		if got.ReasoningEffort != want || response.Session == nil || response.Session.ReasoningEffort != want {
			t.Fatalf("stored=%+v response=%+v", got, response)
		}
		if got.MessageCount != 0 || !got.UpdatedAt.Equal(sess.UpdatedAt) {
			t.Fatalf("setting fabricated conversation activity: %+v", got)
		}
		// Simulate another conversation changing the global preference, then
		// reopening this conversation using its saved runtime.
		_ = llm.SetReasoningEffort("medium")
		if err := llm.SetReasoningEffort(got.ReasoningEffort); err != nil {
			t.Fatal(err)
		}
		if llm.ReasoningEffort() != want {
			t.Fatalf("restored wrong setting")
		}
	}
}

func TestReasoningSelectionRejectsInvalidSessionWithoutChangingRuntime(t *testing.T) {
	t.Cleanup(func() { _ = llm.SetReasoningEffort("") })
	srv := newTestServer(t, false)
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.Create(t.TempDir(), "model", "foreign")
	if err != nil {
		t.Fatal(err)
	}
	_ = llm.SetReasoningEffort("low")
	for _, tc := range []struct {
		id, level string
		status    int
	}{
		{"missing", "high", 404},
		{foreign.ID, "high", 404},
		{foreign.ID, "bad", 400},
	} {
		rec := httptest.NewRecorder()
		body := fmt.Sprintf(`{"session_id":%q,"level":%q}`, tc.id, tc.level)
		srv.handleReasoning(rec, httptest.NewRequest(http.MethodPost, "/api/reasoning", strings.NewReader(body)))
		if rec.Code != tc.status || llm.ReasoningEffort() != "low" {
			t.Fatalf("code=%d effort=%s", rec.Code, llm.ReasoningEffort())
		}
	}
}
