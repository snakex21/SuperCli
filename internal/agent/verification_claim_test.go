package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestClaimsVerification(t *testing.T) {
	for _, text := range []string{
		"Done — INVENTORY.md was created and verified.",
		"All tests pass now.",
		"The build succeeded and tests passed.",
		"go test: tests are passing.",
		"Zmieniłem stałą i zweryfikowałem.",
		"Wszystkie testy przechodzą.",
	} {
		if !claimsVerification(text) {
			t.Errorf("missed claim: %q", text)
		}
	}
	for _, text := range []string{
		"I could not verify the change; tests still fail.",
		"The tests were not verified.",
		"I verified that the file does not exist; which file did you mean?",
		"Tests fail with a contradiction between TestA and TestB.",
		"Nie zweryfikowano zmian.",
		"Summary of the project structure.",
		"This file is unverified.",
	} {
		if claimsVerification(text) {
			t.Errorf("false claim: %q", text)
		}
	}
}

func TestVerificationClaimWarningFollowsLastCheck(t *testing.T) {
	l := &Loop{baseDir: t.TempDir()}
	claim := "Fixed and verified."
	if w := l.verificationClaimWarning(claim); !strings.Contains(w, "no test or build command ran") {
		t.Fatalf("no-check claim not flagged: %q", w)
	}
	call := llm.ToolCall{Name: "ctx_execute", Arguments: `{"command":["go","test","./..."]}`}
	l.recordCheckResult(call, tools.Result{Err: errors.New("FAIL")})
	if w := l.verificationClaimWarning(claim); !strings.Contains(w, "last check in this turn failed: go test ./...") {
		t.Fatalf("failed-check claim not flagged: %q", w)
	}
	l.recordCheckResult(call, tools.Result{Text: `{"exit_code":0}`})
	if w := l.verificationClaimWarning(claim); w != "" {
		t.Fatalf("passing check flagged: %q", w)
	}
	// Unrelated commands are not checks and change nothing.
	l.recordCheckResult(llm.ToolCall{Name: "ctx_execute", Arguments: `{"command":["git","status"]}`}, tools.Result{Err: errors.New("x")})
	if w := l.verificationClaimWarning(claim); w != "" {
		t.Fatalf("non-check command counted: %q", w)
	}
	if w := l.verificationClaimWarning("The file legacy/billing_v1.go does not exist. Which file did you mean?"); w != "" {
		t.Fatalf("honest answer flagged: %q", w)
	}
}

// End to end: a run whose only test failed and whose answer says it passed
// ends with the visible note in the streamed text and in history.
func TestFinalAnswerClaimAfterFailedCheckGetsNote(t *testing.T) {
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "ctx_execute", Description: "fixture", Schema: `{"type":"object","properties":{"command":{"type":"array","items":{"type":"string"}}},"required":["command"]}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		return tools.Result{Err: errors.New("--- FAIL: TestPick")}, nil
	}})
	reg.MarkAlwaysOn("ctx_execute")
	provider := &stubProvider{name: "claim", scripts: [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "t1", Name: "ctx_execute", Arguments: `{"command":["go","test","./..."]}`}}},
		{{Content: "Done. All tests pass.", FinishReason: "stop"}},
	}}
	loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, MaxSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	var streamed strings.Builder
	for _, event := range drainEvents(t, mustRun(t, loop, "Fix the tests.")) {
		switch e := event.(type) {
		case MessageEvent:
			streamed.WriteString(e.Text)
		case ErrorEvent:
			t.Fatal(e.Err)
		}
	}
	if !strings.Contains(streamed.String(), "last check in this turn failed") {
		t.Fatalf("note not streamed: %q", streamed.String())
	}
	last := loop.Messages[len(loop.Messages)-1]
	if last.Role != llm.RoleAssistant || !strings.Contains(last.Content+fmt.Sprint(last.Parts), "SuperCli note") {
		t.Fatalf("note not in history: %+v", last)
	}
}
