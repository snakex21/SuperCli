package webgui

import (
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/tools"
)

func TestApprovalCardReachesTheWireOnlyForTrustedConfirmations(t *testing.T) {
	e := &Engine{questions: make(map[string]tools.AskRequest)}
	card := &tools.ApprovalCard{Tool: "thunderbird_mail", Operation: "copy", ApproveLabel: "Approve", DenyLabel: "Deny"}
	wire := e.registerQuestion(tools.AskRequest{ID: "a1", Question: "q", Confirmation: true, Approval: card,
		Options: []tools.AskOption{{Label: "Deny"}, {Label: "Approve"}}})
	data, _ := json.Marshal(wire)
	if !strings.Contains(string(data), `"approval":{"tool":"thunderbird_mail","operation":"copy"`) || wire.AllowCustom {
		t.Fatalf("trusted approval wire: %s", data)
	}
	wire = e.registerQuestion(tools.AskRequest{ID: "a2", Question: "q", Approval: card, AllowCustom: true,
		Options: []tools.AskOption{{Label: "Approve"}, {Label: "Deny"}}})
	if wire.Approval != nil {
		t.Fatal("an approval card without the confirmation marker reached the UI")
	}
	// Only the offered labels are accepted as answers.
	if err := e.answerQuestion("a1", tools.AskAnswer{Selected: []string{"Allow everything"}}); err == nil {
		t.Fatal("unknown approval label accepted")
	}
}
