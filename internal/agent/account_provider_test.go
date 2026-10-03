package agent

import (
	"reflect"
	"testing"

	"supercli/internal/llm"
)

func TestAccountPoolRefreshPreservesContextAndRejectsModelChange(t *testing.T) {
	plain, _ := llm.NewEcho("fixture-model")
	old, _ := llm.NewRouter(plain)
	next, _ := llm.NewRouter(plain, plain, plain)
	messages := []llm.Message{{Role: llm.RoleUser, Content: "fixture history"}, {Role: llm.RoleAssistant, Content: "fixture response"}}
	l := &Loop{provider: old, modelID: "fixture-model", Messages: messages, contextBaseExact: 12345, contextBaseEstimated: 23456, lastThinking: "fixture"}
	if err := l.SetAccountProvider(next); err != nil {
		t.Fatal(err)
	}
	if l.provider != next || l.CurrentModel() != "fixture-model" || !reflect.DeepEqual(l.Messages, messages) || l.contextBaseExact != 12345 || l.contextBaseEstimated != 23456 || l.lastThinking != "fixture" || l.contextModel.loaded {
		t.Fatal("account count became a context/model handoff")
	}
	other, _ := llm.NewEcho("different-fixture-model")
	if err := l.SetAccountProvider(other); err == nil || l.provider != next {
		t.Fatal("account refresh accepted a different model")
	}
	l.SetModel(old)
	if l.contextBaseExact != 12345 || l.contextModel.loaded || l.CurrentModel() != "fixture-model" {
		t.Fatal("same-model decorated pool name reset context")
	}
}
