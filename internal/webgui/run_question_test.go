package webgui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

type askingProvider struct{ calls int }

func (p *askingProvider) Name() string { return "asking" }
func (p *askingProvider) Complete(_ context.Context, _ []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	p.calls++
	out := make(chan llm.Delta, 3)
	if p.calls == 1 {
		found := false
		for _, def := range defs {
			if def.Name == "ask_user" {
				found = true
			}
		}
		if !found {
			panic("ask_user missing from web registry")
		}
		out <- llm.Delta{Role: llm.RoleAssistant, ToolCall: &llm.ToolCall{ID: "ask-call", Name: "ask_user", Arguments: `{"question":"Choose","options":[{"label":"A"},{"label":"B"}]}`}}
		out <- llm.Delta{FinishReason: "tool_calls"}
	} else {
		out <- llm.Delta{Role: llm.RoleAssistant, Content: "continued after choice"}
		out <- llm.Delta{FinishReason: "stop", Usage: &llm.Usage{Input: 10, Output: 3, Total: 13}}
	}
	close(out)
	return out, nil
}

func TestQuestionAnswerKeepsRequestAfterInvalidRetry(t *testing.T) {
	eng, err := NewEngine(echoConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	replies := make(chan tools.AskAnswer, 1)
	req := tools.AskRequest{ID: "ask-test", Question: "Pick", AllowCustom: true,
		Options: []tools.AskOption{{Label: "A"}, {Label: "B"}}, Respond: replies}
	eng.registerQuestion(req)
	if err := eng.answerQuestion(req.ID, tools.AskAnswer{Selected: []string{"missing"}}); err == nil {
		t.Fatal("unknown option should fail")
	}
	if err := eng.answerQuestion(req.ID, tools.AskAnswer{Custom: "my own"}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-replies:
		if got.Custom != "my own" {
			t.Fatalf("answer = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("answer was not delivered")
	}
}

func TestQuestionAnswerEndpoint(t *testing.T) {
	srv := newTestServer(t, false)
	replies := make(chan tools.AskAnswer, 1)
	srv.eng.registerQuestion(tools.AskRequest{ID: "ask-http", Question: "Pick", AllowCustom: true,
		Options: []tools.AskOption{{Label: "A"}, {Label: "B"}}, Respond: replies})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/question/answer", strings.NewReader(`{"id":"ask-http","selected":["B"],"custom":"note"}`))
	req.Host = "127.0.0.1:8080"
	req.RemoteAddr = "127.0.0.1:43210"
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]bool
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || !body["ok"] {
		t.Fatalf("body=%s err=%v", rec.Body.String(), err)
	}
	select {
	case got := <-replies:
		if len(got.Selected) != 1 || got.Selected[0] != "B" || got.Custom != "note" {
			t.Fatalf("answer=%+v", got)
		}
	case <-context.Background().Done():
	}
}

func TestRunStreamQuestionPausesProgressTimeout(t *testing.T) {
	dir := t.TempDir()
	cfg := echoConfig()
	cfg.Timeout = 20 * time.Millisecond
	eng, err := NewEngine(cfg, dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	eng.mu.Lock()
	eng.prov = &askingProvider{}
	eng.mu.Unlock()

	answered := make(chan struct{})
	err = eng.runStream(context.Background(), "ask me", "", "", func(ev wireEvent) {
		if ev.Type != "question" || ev.Question == nil {
			return
		}
		q := *ev.Question
		go func() {
			time.Sleep(60 * time.Millisecond) // deliberately longer than provider watchdog
			if answerErr := eng.answerQuestion(q.ID, tools.AskAnswer{Selected: []string{"B"}}); answerErr != nil {
				t.Errorf("answerQuestion: %v", answerErr)
			}
			close(answered)
		}()
	})
	if err != nil {
		t.Fatalf("runStream timed out while waiting for user: %v", err)
	}
	select {
	case <-answered:
	case <-time.After(time.Second):
		t.Fatal("question was not answered")
	}
}

func TestRunStreamPresentsAndResumesAskUser(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	eng.mu.Lock()
	eng.prov = &askingProvider{}
	eng.mu.Unlock()
	var sawQuestion, sawResult, sawDone bool
	err = eng.runStream(context.Background(), "ask me", "", "", func(ev wireEvent) {
		switch ev.Type {
		case "question":
			sawQuestion = ev.Question != nil && ev.Question.AllowCustom
			if err := eng.answerQuestion(ev.Question.ID, tools.AskAnswer{Selected: []string{"B"}}); err != nil {
				t.Error(err)
			}
		case "tool_result":
			sawResult = strings.Contains(ev.Output, "user selected: B")
		case "done":
			sawDone = true
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawQuestion || !sawResult || !sawDone {
		t.Fatalf("question flow: question=%v result=%v done=%v", sawQuestion, sawResult, sawDone)
	}
}
