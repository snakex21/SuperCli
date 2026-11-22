package webgui

import (
	"context"
	"strings"
	"supercli/internal/llm"
	"supercli/internal/tools"
	"testing"
	"time"
)

type resumedQuestionProvider struct{}

func (*resumedQuestionProvider) Name() string { return "resume-fixture" }
func (*resumedQuestionProvider) Complete(_ context.Context, messages []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	out := make(chan llm.Delta, 3)
	user := ""
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llm.RoleUser {
			user = messages[i].Content
			break
		}
	}
	last := messages[len(messages)-1]
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != llm.RoleSystem {
			last = messages[i]
			break
		}
	}
	var call *llm.ToolCall
	// An automatic turn reads a worker report: answer, never delegate again.
	if last.Role == llm.RoleUser && last.Name != llm.WorkerNotificationName && !strings.Contains(user, "<task-notification>") {
		switch {
		case strings.HasPrefix(strings.TrimSpace(user), "worker-fixture-resume"):
			call = &llm.ToolCall{ID: "question-call", Name: "ask_user", Arguments: `{"question":"Resume choice?","options":[{"label":"A"},{"label":"B"}]}`}
		case strings.HasPrefix(strings.TrimSpace(user), "worker-fixture-start"):
		case strings.Contains(user, "continue-fixture"):
			call = &llm.ToolCall{ID: "resume-call", Name: "send_message", Arguments: `{"to":"worker-1","message":"worker-fixture-resume"}`}
		default:
			call = &llm.ToolCall{ID: "spawn-call", Name: "task", Arguments: `{"prompt":"worker-fixture-start"}`}
		}
	}
	if call != nil {
		out <- llm.Delta{ToolCall: call}
		out <- llm.Delta{FinishReason: "tool_calls"}
	} else {
		out <- llm.Delta{Content: "completed fixture"}
		out <- llm.Delta{FinishReason: "stop"}
	}
	close(out)
	return out, nil
}

// resumeWorkerInNewWebRun drives the shared scenario: a first web run
// delegates worker-1, a second (new) web run continues it with send_message.
// The continuation runs in the background (1.0.39): the second run returns at
// once, and the worker's question, live progress and final report reach the
// conversation's views through its background frames.
func resumeWorkerInNewWebRun(t *testing.T, eng *Engine) (continued wireEvent, events []wireEvent) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	rec := newSessionEventRecorder(t, eng)
	if err := eng.runStream(ctx, "implement spawn-fixture", "", "", rec.emit); err != nil {
		t.Fatal(err)
	}
	sid := rec.session()
	rec.waitFor(t, 10*time.Second, "worker-1's first report", func(evs []wireEvent) bool {
		for _, ev := range evs {
			if ev.Type == "worker" && ev.ID == "worker-1" && ev.Status == "done" {
				return true
			}
		}
		return false
	})
	// Wait until the automatic turn for that report has released the
	// conversation, so the next message is a plain new run.
	deadline := time.Now().Add(10 * time.Second)
	for eng.sessionRunActive(sid) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	started := time.Now()
	if err := eng.runStream(ctx, "implement continue-fixture", sid, "", rec.emit); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(started); took > 5*time.Second {
		t.Fatalf("the continuing run waited %v for the worker", took)
	}
	for _, ev := range rec.snapshot() {
		if ev.Type == "tool_result" && ev.ID == "resume-call" {
			continued = ev
		}
	}
	// The worker asks after the run that continued it has ended.
	var question *questionWire
	rec.waitFor(t, 10*time.Second, "the resumed worker's question", func(evs []wireEvent) bool {
		for _, ev := range evs {
			if ev.Type == "question" && ev.Question != nil {
				question = ev.Question
				return true
			}
		}
		return false
	})
	if err := eng.answerQuestion(question.ID, tools.AskAnswer{Selected: []string{"B"}}); err != nil {
		t.Fatal(err)
	}
	events = rec.waitFor(t, 10*time.Second, "the resumed worker's report", func(evs []wireEvent) bool {
		for _, ev := range evs {
			if ev.Type == "worker" && ev.ID == "worker-1" && ev.Run == 2 && ev.Status == "done" {
				return true
			}
		}
		return false
	})
	return continued, events
}

func TestResumedWorkerQuestionAndProgressUseNewWebRun(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	eng.prov = &resumedQuestionProvider{}
	continued, events := resumeWorkerInNewWebRun(t, eng)
	if continued.Err != "" || !strings.Contains(continued.Output, "continued in background") {
		t.Fatalf("continuation result = %+v", continued)
	}
	var questionClosed, progress, report bool
	for _, ev := range events {
		if ev.Type == "question_closed" {
			questionClosed = true
		}
		// Progress of the new run (2) is bound to the call that continued it.
		if ev.Type == "worker_progress" && ev.ParentCallID == "resume-call" && ev.Run == 2 {
			progress = true
		}
		if ev.Type == "worker" && ev.ID == "worker-1" && ev.Run == 2 && strings.Contains(ev.Text+ev.Output, "completed fixture") {
			report = true
		}
	}
	if !progress || !report {
		t.Fatalf("progress=%v report=%v questionClosed=%v", progress, report, questionClosed)
	}
}
