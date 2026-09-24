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
	if last.Role == llm.RoleUser {
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

func TestResumedWorkerQuestionAndProgressUseNewWebRun(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	eng.prov = &resumedQuestionProvider{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sid := ""
	if err := eng.runStream(ctx, "implement spawn-fixture", "", "", func(ev wireEvent) {
		if ev.Type == "session" {
			sid = ev.SessionID
		}
	}); err != nil {
		t.Fatal(err)
	}
	var question, progress, result bool
	err = eng.runStream(ctx, "implement continue-fixture", sid, "", func(ev wireEvent) {
		if ev.Type == "question" {
			question = true
			if err := eng.answerQuestion(ev.Question.ID, tools.AskAnswer{Selected: []string{"B"}}); err != nil {
				t.Error(err)
			}
		}
		if ev.Type == "worker_progress" && ev.ParentCallID == "resume-call" && ev.Run == 2 {
			progress = true
		}
		if ev.Type == "tool_result" && ev.ID == "resume-call" && ev.Err == "" && strings.Contains(ev.Output, "completed fixture") {
			result = true
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !question || !progress || !result {
		t.Fatalf("question=%v progress=%v result=%v", question, progress, result)
	}
}
