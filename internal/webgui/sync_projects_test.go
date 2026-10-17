package webgui

import (
	"testing"
)

func TestSyncOtherProjectSummariesLiveAndReconnect(t *testing.T) {
	var hub syncHub
	pc, snapshot, unsubPC := hub.subscribe("selected-in-b", "pc")
	defer unsubPC()
	phone, _, unsubPhone := hub.subscribe("selected-in-b", "phone")
	defer unsubPhone()
	if len(snapshot.Runs) != 0 {
		t.Fatal("unexpected initial runs")
	}
	run := hub.start("conversation-a", "prompt", syncIdentity{turnID: "turn-a"}, func() {}, syncFrame{ProjectPath: "/project/a", ProjectName: "Project A", Title: "Conversation A"})
	var started int64
	for _, sub := range []*syncSubscriber{pc, phone} {
		frame := <-sub.events
		if frame.Type != "run_started" || frame.Title != "Conversation A" || frame.ProjectName != "Project A" || frame.ProjectPath != "/project/a" || frame.State != "running" || frame.StartedAtMS <= 0 {
			t.Fatalf("start summary: %+v", frame)
		}
		started = frame.StartedAtMS
		<-sub.events // sessions_changed
	}
	hub.event(run, wireEvent{Type: "message", Text: "private transcript"})
	for _, sub := range []*syncSubscriber{pc, phone} {
		select {
		case frame := <-sub.events:
			t.Fatalf("other project's content leaked: %+v", frame)
		default:
		}
	}
	hub.event(run, wireEvent{Type: "question", Question: &questionWire{ID: "q1"}})
	for _, sub := range []*syncSubscriber{pc, phone} {
		frame := <-sub.events
		if frame.Type != "run_updated" || frame.State != "waiting_answer" || frame.StartedAtMS != started || frame.Event != nil {
			t.Fatalf("waiting summary: %+v", frame)
		}
	}
	_, snapshot, unsub := hub.subscribe("selected-in-b", "reconnected")
	defer unsub()
	if len(snapshot.Runs) != 1 || snapshot.Runs[0].State != "waiting_answer" || snapshot.Runs[0].StartedAtMS != started || len(snapshot.Events) != 0 {
		t.Fatalf("reconnect: %+v", snapshot)
	}
	hub.event(run, wireEvent{Type: "question_closed", ID: "q1"})
	for _, sub := range []*syncSubscriber{pc, phone} {
		if frame := <-sub.events; frame.State != "running" {
			t.Fatalf("answer summary: %+v", frame)
		}
	}
	hub.finish(run, nil)
	for _, sub := range []*syncSubscriber{pc, phone} {
		if frame := <-sub.events; frame.Type != "run_finished" {
			t.Fatalf("finish summary: %+v", frame)
		}
	}
	_, snapshot, done := hub.subscribe("", "")
	defer done()
	if len(snapshot.Runs) != 0 {
		t.Fatal("finished run remains listed")
	}
}

func TestSyncRunDetailsUsesStoredConversationAndProjectNames(t *testing.T) {
	s := newTestServer(t, false)
	store, err := s.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Create(s.eng.Home(), "model", "Stored conversation title")
	if err != nil {
		t.Fatal(err)
	}
	info := s.eng.syncRunDetails(session.ID, s.eng.Home(), "new prompt")
	if info.Title != "Stored conversation title" || info.ProjectPath != s.eng.Home() || info.ProjectName == "" {
		t.Fatalf("metadata: %+v", info)
	}
}
