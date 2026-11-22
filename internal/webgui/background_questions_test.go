package webgui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"supercli/internal/tools"
)

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A background worker's question stays pending for its conversation until it
// is answered: a view that opens the conversation later (or reloads) gets it
// from /api/questions, and the session list gets a count (1.0.39).
func TestBackgroundQuestionIsPersistentUntilAnswered(t *testing.T) {
	srv := newTestServer(t, false)
	bg := srv.eng.backgroundFor("sess-q", srv.eng.Home())
	respond := make(chan tools.AskAnswer, 1)
	done := make(chan struct{})
	bg.askChannel() <- tools.AskRequest{ID: "q-1", Question: "Which texture?", Options: []tools.AskOption{{Label: "A"}, {Label: "B"}}, Respond: respond, Done: done}
	waitUntil(t, "pending question", func() bool { return len(srv.eng.PendingQuestions("sess-q")) == 1 })

	sub, snapshot, unsubscribe := srv.eng.syncHub.subscribe("other-view", "c")
	defer unsubscribe()
	_ = sub
	if snapshot.Questions["sess-q"] != 1 {
		t.Fatalf("snapshot question counts = %v", snapshot.Questions)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/questions?session=sess-q", nil)
	req.Host, req.RemoteAddr = "127.0.0.1", "127.0.0.1:5000"
	srv.Handler().ServeHTTP(rec, req)
	var got struct {
		Questions []questionWire `json:"questions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Questions) != 1 || got.Questions[0].ID != "q-1" {
		t.Fatalf("GET /api/questions = %d %s", rec.Code, rec.Body.String())
	}
	if err := srv.eng.answerQuestion("q-1", tools.AskAnswer{Selected: []string{"B"}}); err != nil {
		t.Fatal(err)
	}
	if ans := <-respond; len(ans.Selected) != 1 || ans.Selected[0] != "B" {
		t.Fatalf("answer = %+v", ans)
	}
	close(done) // the asking tool finishes
	waitUntil(t, "question removed", func() bool { return len(srv.eng.PendingQuestions("sess-q")) == 0 })
	srv.eng.questionMu.Lock()
	_, still := srv.eng.questions["q-1"]
	srv.eng.questionMu.Unlock()
	if still {
		t.Fatal("answered question left registered")
	}
}

// A request without its own Done is never left registered forever: answered
// ones are noticed, unanswered ones end after a bounded lifetime.
func TestBackgroundQuestionWithoutDoneIsCleanedUp(t *testing.T) {
	oldAge, oldPoll := backgroundQuestionMaxAge, backgroundQuestionPoll
	backgroundQuestionMaxAge, backgroundQuestionPoll = 150*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { backgroundQuestionMaxAge, backgroundQuestionPoll = oldAge, oldPoll })
	srv := newTestServer(t, false)
	bg := srv.eng.backgroundFor("sess-nodone", srv.eng.Home())
	respond := make(chan tools.AskAnswer, 1)
	bg.askChannel() <- tools.AskRequest{ID: "q-2", Question: "Continue?", Options: []tools.AskOption{{Label: "Yes"}, {Label: "No"}}, Respond: respond}
	waitUntil(t, "pending question", func() bool { return len(srv.eng.PendingQuestions("sess-nodone")) == 1 })
	waitUntil(t, "expired question removed", func() bool {
		srv.eng.questionMu.Lock()
		_, open := srv.eng.questions["q-2"]
		srv.eng.questionMu.Unlock()
		return !open && len(srv.eng.PendingQuestions("sess-nodone")) == 0
	})
	select {
	case ans := <-respond:
		if !ans.Cancelled {
			t.Fatalf("expired question answered %+v, want cancelled", ans)
		}
	default:
		t.Fatal("the asker was never told the question expired")
	}
}
