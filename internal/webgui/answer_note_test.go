package webgui

import (
	"context"
	"strings"
	"testing"

	"supercli/internal/llm"
)

type claimProvider struct{ answer string }

func (*claimProvider) Name() string { return "claim-fixture" }
func (p *claimProvider) Complete(context.Context, []llm.Message, []llm.ToolDef) (<-chan llm.Delta, error) {
	return workerSteeringWireDeltas(llm.Delta{Content: p.answer, FinishReason: "stop"}), nil
}

// "Testy przechodzą" with no command run by anyone gets a translated badge
// (answer_note), not a raw "[SuperCli note: ...]" in the answer; the saved
// transcript keeps the note beside the text for a reload, and the answer
// text itself stays exactly what the model said.
func TestAnswerNoteIsABadgeNotText(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	eng.prov = &claimProvider{answer: "Gotowe, wszystkie testy przechodzą."}
	var text strings.Builder
	var notes []wireEvent
	sid := ""
	if err := eng.runStream(context.Background(), "Napraw testy.", "", "", func(ev wireEvent) {
		switch ev.Type {
		case "session":
			sid = ev.SessionID
		case "message":
			text.WriteString(ev.Text)
		case "answer_note":
			notes = append(notes, ev)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text.String(), "SuperCli note") || strings.Contains(text.String(), "[") {
		t.Fatalf("raw marker in the answer: %q", text.String())
	}
	if len(notes) != 1 || notes[0].Kind != llm.NoteUnverifiedClaim {
		t.Fatalf("answer notes = %+v", notes)
	}
	msgs, err := eng.transcript(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range msgs {
		if m.Role == string(llm.RoleAssistant) {
			if strings.Contains(m.Content, "SuperCli note") {
				t.Fatalf("raw marker saved in the answer: %q", m.Content)
			}
			for _, n := range m.Notes {
				found = found || n.Kind == llm.NoteUnverifiedClaim
			}
		}
	}
	if !found {
		t.Fatal("the saved transcript lost the note")
	}
}
