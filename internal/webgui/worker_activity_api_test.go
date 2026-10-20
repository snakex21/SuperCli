package webgui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"supercli/internal/agent"
)

func TestWorkerActivityAPIJSON(t *testing.T) {
	updated := time.UnixMilli(1792357500123)
	w := &agent.Worker{
		ID: "worker-activity", Agent: "explore", Description: "inspect", Model: "test-model",
		Status: "done", Runs: 3, UpdatedAt: updated, TokensIn: 10, TokensOut: 20, Steps: 2,
		ToolNames: []string{"search_code"}, LastResult: strings.Repeat("PRIVATE_FULL_REPORT", 1000),
	}
	s := w.Snapshot()
	s.Activity = agent.WorkerActivity{
		Kind: "tool_result", Tool: "search_code", CallID: "latest-call",
		Args: strings.Repeat("x", 1024), At: updated.UnixMilli() - 1,
	}
	row := workerDTO(s)
	encoded, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["run"]) != "3" || string(fields["updated_at"]) != "1792357500123" {
		t.Fatalf("missing run/time: %s", encoded)
	}
	var activity agent.WorkerActivity
	if err := json.Unmarshal(fields["activity"], &activity); err != nil {
		t.Fatal(err)
	}
	if activity != s.Activity || len(activity.Args) > 1024 || !utf8.ValidString(activity.Args) {
		t.Fatalf("activity changed or unbounded: %+v", activity)
	}
	var activityFields map[string]json.RawMessage
	if err := json.Unmarshal(fields["activity"], &activityFields); err != nil {
		t.Fatal(err)
	}
	if len(activityFields) != 5 || string(activityFields["call_id"]) != `"latest-call"` || string(activityFields["at"]) != "1792357500122" {
		t.Fatalf("unexpected activity JSON fields: %s", fields["activity"])
	}
	for _, forbidden := range []string{"LastResult", "last_result", "PRIVATE_FULL_REPORT", "output", "reasoning"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Errorf("API leaked %q: %s", forbidden, encoded)
		}
	}
	if row.ID != s.ID || row.Status != s.Status || row.Model != s.Model || row.TokensIn != 10 || row.TokensOut != 20 || row.Steps != 2 || len(row.ToolNames) != 1 {
		t.Fatalf("existing fields changed: %+v", row)
	}
	row.Activity.Args = "reader edit"
	if s.Activity.Args != strings.Repeat("x", 1024) {
		t.Fatal("DTO activity aliases the input snapshot")
	}
	encoded, err = json.Marshal(workerDTO(w.Snapshot()))
	if err != nil || strings.Contains(string(encoded), `"activity"`) {
		t.Fatalf("empty activity not omitted: %s, %v", encoded, err)
	}
	if !strings.Contains(string(encoded), `"run":3`) || !strings.Contains(string(encoded), `"updated_at":1792357500123`) {
		t.Fatalf("run/time missing without activity: %s", encoded)
	}
}
