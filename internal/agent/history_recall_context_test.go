package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

type historyRecallWriter struct{ *flakyWriter }

func (*historyRecallWriter) SessionID() string { return "owned-archive" }

func TestHistoryRecallAddressIsSmallFreshAndOnlyInRequestTail(t *testing.T) {
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "search_history", Description: "Read prior evidence.", Schema: `{"type":"object"}`, ReadOnly: true, Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }})
	l, err := NewLoop(LoopConfig{Provider: &stubProvider{name: "fixture"}, Registry: reg, Writer: &historyRecallWriter{&flakyWriter{}}, System: "fixed instructions"})
	if err != nil {
		t.Fatal(err)
	}
	l.route = RouteCoordinator
	l.LoadConversation([]llm.Message{{Role: llm.RoleUser, Content: WrapCompactSummary("Done: verified change. State: keep restrictions.")}, {Role: llm.RoleUser, Content: "Continue the work."}})
	request := l.providerMessages()
	for i, m := range request {
		if strings.Contains(m.Content, "owned-archive") && i != len(request)-1 {
			t.Fatal("archive pointer changed the stable prefix")
		}
	}
	if hint := l.historyRecallContext(); !strings.Contains(hint, `session_id="owned-archive"`) || len(hint) > 512 {
		t.Fatal("targeted archive address missing or unbounded")
	}
	for _, m := range l.Messages {
		if strings.Contains(m.Content, "owned-archive") {
			t.Fatal("request-only archive pointer persisted in history")
		}
	}
	l.LoadConversation([]llm.Message{{Role: llm.RoleUser, Content: "A new short conversation."}})
	if l.historyRecallContext() != "" {
		t.Fatal("stale archive hint survived replacing the conversation")
	}
}
