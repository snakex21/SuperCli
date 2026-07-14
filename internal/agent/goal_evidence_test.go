package agent

import (
	"encoding/json"
	"testing"
)

func TestPassingGoalVerificationDetection(t *testing.T) {
	if !isPassingGoalVerification("goal", json.RawMessage(`{"action":"verify","passed":true,"text":"tests passed"}`)) {
		t.Fatal("passing goal verification was not detected")
	}
	for _, raw := range []string{`{"action":"verify","passed":false}`, `{"action":"show"}`, `{bad`} {
		if isPassingGoalVerification("goal", json.RawMessage(raw)) {
			t.Fatalf("non-passing args detected: %s", raw)
		}
	}
	if isPassingGoalVerification("ctx_execute", json.RawMessage(`{"passed":true}`)) {
		t.Fatal("non-goal tool detected")
	}
}

func TestConcreteEvidenceToolExcludesMetaActions(t *testing.T) {
	for _, name := range []string{"goal", "tool_search", "recall", "ask_user"} {
		if isConcreteEvidenceTool(name) {
			t.Errorf("meta tool %q counted as evidence", name)
		}
	}
	for _, name := range []string{"ctx_execute", "read_image", "read_lines", "task"} {
		if !isConcreteEvidenceTool(name) {
			t.Errorf("concrete tool %q not counted as evidence", name)
		}
	}
}
