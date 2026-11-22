package webgui

import (
	"time"

	"supercli/internal/tools"
)

// Questions of background workers (1.0.39). A delegated worker runs detached
// from the chat run that started it, so by the time it calls ask_user (or
// needs an approval) that run's question channel has no reader any more.
// Each conversation therefore owns one durable question route: requests are
// registered like a run's questions (same /api/question/answer endpoint) and
// published to every view of the conversation through the sync hub, so the
// question appears whether or not a turn is running. An unanswered question
// ends with the asking tool's own timeout, and the view is told to close it.

// backgroundAskBuffer bounds questions queued before the pump publishes them.
const backgroundAskBuffer = 8

func (bg *sessionBackground) askChannel() chan<- tools.AskRequest {
	if bg == nil {
		return nil
	}
	return bg.ask
}

func (bg *sessionBackground) askPump() {
	for req := range bg.ask {
		if req.Done != nil {
			select {
			case <-req.Done:
				continue // expired while queued
			default:
			}
		}
		question := bg.e.registerQuestion(req)
		bg.e.syncHub.background(bg.sid, wireEvent{Type: "question", Question: &question, At: time.Now().UnixMilli()})
		if req.Done == nil {
			continue
		}
		go func(id string, done <-chan struct{}) {
			<-done
			bg.e.questionMu.Lock()
			delete(bg.e.questions, id)
			bg.e.questionMu.Unlock()
			bg.e.syncHub.background(bg.sid, wireEvent{Type: "question_closed", ID: id})
		}(req.ID, req.Done)
	}
}
