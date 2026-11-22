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
		"I verified this by running go test ./..., and each possible behavior fails one of the tests.",
		"<thinking>verify that all tests pass</thinking>The tests contradict each other.",
	} {
		if claimsVerification(text) {
			t.Errorf("false claim: %q", text)
		}
	}
}

func noteOf(n *llm.AnswerNote) string {
	if n == nil {
		return ""
	}
	return n.Kind + ":" + n.Detail
}

func TestVerificationClaimNoteFollowsLastCheck(t *testing.T) {
	l := &Loop{baseDir: t.TempDir()}
	claim := "Fixed and verified."
	if w := noteOf(l.verificationClaimNote(claim)); w != llm.NoteUnverifiedClaim+":" {
		t.Fatalf("no-check claim not flagged: %q", w)
	}
	call := llm.ToolCall{Name: "ctx_execute", Arguments: `{"command":["go","test","./..."]}`}
	l.recordCheckResult(call, tools.Result{Err: errors.New("FAIL")})
	if w := noteOf(l.verificationClaimNote(claim)); w != llm.NoteFailedCheck+":go test ./..." {
		t.Fatalf("failed-check claim not flagged: %q", w)
	}
	l.recordCheckResult(call, tools.Result{Text: `{"exit_code":0}`})
	if w := noteOf(l.verificationClaimNote(claim)); w != "" {
		t.Fatalf("passing check flagged: %q", w)
	}
	// Unrelated commands are not checks and change nothing.
	l.recordCheckResult(llm.ToolCall{Name: "ctx_execute", Arguments: `{"command":["git","status"]}`}, tools.Result{Err: errors.New("x")})
	if w := noteOf(l.verificationClaimNote(claim)); w != "" {
		t.Fatalf("non-check command counted: %q", w)
	}
	// Checks inside compound shell lines count (seen with a local model).
	compound := llm.ToolCall{Name: "ctx_execute", Arguments: `{"command":["cmd","/c","cd /d x && go test ./..."]}`}
	l.recordCheckResult(compound, tools.Result{Text: `{"exit_code":1}`})
	if w := noteOf(l.verificationClaimNote(claim)); w != llm.NoteFailedCheck+":cmd /c cd /d x && go test ./..." {
		t.Fatalf("compound check failure missed: %q", w)
	}
	l.recordCheckResult(llm.ToolCall{Name: "ctx_execute", Arguments: `{"command":["cmd","/c","node","--test"]}`}, tools.Result{Text: `{"exit_code":0}`})
	if w := noteOf(l.verificationClaimNote(claim)); w != "" {
		t.Fatalf("passing node --test flagged: %q", w)
	}
	// A command that is not a recognisable check may be the verification.
	other := &Loop{baseDir: t.TempDir()}
	other.recordCheckResult(llm.ToolCall{Name: "ctx_execute", Arguments: `{"command":["go","run",".","--help"]}`}, tools.Result{Text: `{"exit_code":0}`})
	if w := noteOf(other.verificationClaimNote(claim)); w != "" {
		t.Fatalf("unrecognised command treated as no evidence: %q", w)
	}
	if w := noteOf(l.verificationClaimNote("The file legacy/billing_v1.go does not exist. Which file did you mean?")); w != "" {
		t.Fatalf("honest answer flagged: %q", w)
	}
}

// The coordinator relays its workers' results: a worker that ran commands
// is evidence, so "tests pass" is not flagged; with no command by anyone it is.
func TestVerificationClaimCountsWorkersCommands(t *testing.T) {
	l := &Loop{baseDir: t.TempDir()}
	var workers []Snapshot
	l.workerStatusSource = func() []Snapshot { return workers }
	claim := "Worker-3 zgłosił: testy przechodzą, build zielony."
	if w := noteOf(l.verificationClaimNote(claim)); w != llm.NoteUnverifiedClaim+":" {
		t.Fatalf("nobody ran anything, want the note; got %q", w)
	}
	workers = []Snapshot{{ID: "worker-3", Status: "done", ToolNames: []string{"read_lines", "ctx_execute", "patch_file"}}}
	if w := noteOf(l.verificationClaimNote(claim)); w != "" {
		t.Fatalf("a worker ran the tests, no note expected; got %q", w)
	}
}

// End to end: a run whose only test failed and whose answer says it passed
// gets a UI note; the answer text and the model history stay untouched.
func TestFinalAnswerClaimAfterFailedCheckGetsNote(t *testing.T) {
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "ctx_execute", Description: "fixture", Schema: `{"type":"object","properties":{"command":{"type":"array","items":{"type":"string"}}},"required":["command"]}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		return tools.Result{Err: errors.New("--- FAIL: TestPick")}, nil
	}})
	reg.MarkAlwaysOn("ctx_execute")
	provider := &stubProvider{name: "claim", scripts: [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "t1", Name: "ctx_execute", Arguments: `{"command":["go","test","./..."]}`}}},
		{{Content: "Done. All tests pass.", FinishReason: "stop"}},
		{{Content: "Next answer.", FinishReason: "stop"}},
	}}
	loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, MaxSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	var streamed strings.Builder
	var notes []AnswerNoteEvent
	for _, event := range drainEvents(t, mustRun(t, loop, "Fix the tests.")) {
		switch e := event.(type) {
		case MessageEvent:
			streamed.WriteString(e.Text)
		case AnswerNoteEvent:
			notes = append(notes, e)
		case ErrorEvent:
			t.Fatal(e.Err)
		}
	}
	if len(notes) != 1 || notes[0].Kind != llm.NoteFailedCheck || notes[0].Detail != "go test ./..." {
		t.Fatalf("notes = %+v", notes)
	}
	if strings.Contains(streamed.String(), "SuperCli note") || strings.Contains(streamed.String(), "[") {
		t.Fatalf("raw marker in the answer text: %q", streamed.String())
	}
	last := loop.Messages[len(loop.Messages)-1]
	if last.Role != llm.RoleAssistant || last.TextOnly().Content != "Done. All tests pass." || last.Origin == nil || len(last.Origin.Notes) != 1 {
		t.Fatalf("history: content=%q notes=%+v", last.Content, last.Origin)
	}
	// The next request carries no trace of the note: nothing to apologize for.
	for _, event := range drainEvents(t, mustRun(t, loop, "Thanks.")) {
		_ = event
	}
	for _, m := range provider.reqs[len(provider.reqs)-1] {
		if strings.Contains(m.Content+fmt.Sprint(m.Parts), "SuperCli note") || strings.Contains(m.Content+fmt.Sprint(m.Parts), NoteMarkerProbe) {
			t.Fatalf("the note reached the model: %q", m.Content)
		}
	}
}

// NoteMarkerProbe is a substring of every note kind; none may reach the model.
const NoteMarkerProbe = "unverified_claim"
