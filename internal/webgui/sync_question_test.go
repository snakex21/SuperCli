package webgui

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"supercli/internal/llm"
)

type syncQuestionProvider struct {
	syncTestProvider
	calls atomic.Int32
}

func (p *syncQuestionProvider) Complete(ctx context.Context, msgs []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	if p.calls.Add(1) == 1 {
		out := make(chan llm.Delta, 2)
		out <- llm.Delta{ToolCall: &llm.ToolCall{ID: "ask-sync", Name: "ask_user", Arguments: `{"question":"Continue?","options":[{"label":"Yes"},{"label":"No"}]}`}}
		out <- llm.Delta{FinishReason: "tool_calls"}
		close(out)
		return out, nil
	}
	return p.syncTestProvider.Complete(ctx, msgs, defs)
}
func readSyncEvent(t *testing.T, r syncTestReader, kind string) wireEvent {
	t.Helper()
	for i := 0; i < 200; i++ {
		frame, err := r.read()
		if err != nil {
			t.Fatal(err)
		}
		if frame.Event != nil && frame.Event.Type == kind {
			return *frame.Event
		}
	}
	t.Fatalf("missing event %s", kind)
	return wireEvent{}
}
func TestSyncQuestionAnsweredFromOtherDeviceAfterSenderDisconnect(t *testing.T) {
	s := newTestServer(t, false)
	setupTestPhone(t, s)
	cookie := pairTestPhone(t, s)
	p := &syncQuestionProvider{syncTestProvider: syncTestProvider{make(chan struct{}), make(chan struct{}), make(chan struct{})}}
	s.eng.prov = p
	api := httptest.NewServer(s.Handler())
	defer api.Close()
	defer s.eng.stopChatRuns()
	global := openSyncSSE(t, api, "")
	defer global.close()
	readSyncUntil(t, global, "snapshot")
	resp, err := http.Post(api.URL+"/api/chat", "application/json", strings.NewReader(`{"prompt":"ask then continue","turn_id":"question-turn"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	start := readSyncUntil(t, global, "run_started")
	pc := openSyncSSE(t, api, start.SessionID)
	defer pc.close()
	snap := readSyncUntil(t, pc, "snapshot")
	var question wireEvent
	for _, ev := range snap.Events {
		if ev.Type == "question" {
			question = ev
		}
	}
	if question.Question == nil {
		question = readSyncEvent(t, pc, "question")
	}
	phone := openSyncWS(t, api, start.SessionID, cookie)
	defer phone.close()
	snap = readSyncUntil(t, phone, "snapshot")
	found := false
	for _, ev := range snap.Events {
		found = found || ev.Type == "question"
	}
	if !found {
		t.Fatal("pending question missing from phone replay")
	}
	resp.Body.Close()
	req, _ := http.NewRequest("POST", api.URL+"/api/question/answer", strings.NewReader(fmt.Sprintf(`{"id":%q,"selected":["Yes"]}`, question.Question.ID)))
	req.Host = "test-phone.trycloudflare.com"
	req.Header.Set("Cf-Ray", "test")
	req.AddCookie(cookie)
	answer, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(answer.Body)
	answer.Body.Close()
	if answer.StatusCode != 200 {
		t.Fatalf("answer: %d %s", answer.StatusCode, body)
	}
	if readSyncEvent(t, pc, "question_closed").ID != question.Question.ID {
		t.Fatal("wrong question closed")
	}
	readSyncEvent(t, phone, "question_closed")
	waitCompletionTest(t, p.entered, "post-answer model")
	replay := openSyncWS(t, api, start.SessionID, cookie)
	defer replay.close()
	for _, ev := range readSyncUntil(t, replay, "snapshot").Events {
		if ev.Type == "question" {
			t.Fatal("answered question replayed")
		}
	}
	close(p.release)
	readSyncUntil(t, pc, "run_finished")
	readSyncUntil(t, phone, "run_finished")
	if s.eng.hasPendingQuestion() {
		t.Fatal("question leaked")
	}
}
