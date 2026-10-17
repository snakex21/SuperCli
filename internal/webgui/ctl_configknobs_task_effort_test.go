package webgui

import (
	"encoding/json"
	"net/http"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

func TestTaskReasoningEffortKnobRoundtrip(t *testing.T) {
	srv := newTestServer(t, false)
	path, _ := config.FindTomlPaths(srv.eng.DataDir(), srv.eng.Home())
	initial := llm.ReasoningEffort()
	for _, value := range append(append([]string{}, llm.ReasoningEffortLevels...), "default", "high", "") {
		body, _ := json.Marshal(map[string]string{"key": "task_reasoning_effort", "value": value})
		rec := knobsPOST(t, srv, string(body))
		if rec.Code != http.StatusOK {
			t.Fatalf("%q: %d %s", value, rec.Code, rec.Body.String())
		}
		want := value
		if want == "default" {
			want = ""
		}
		got, err := config.LoadToml(path)
		if err != nil {
			t.Fatal(err)
		}
		if got.TaskReasoningEffort != want {
			t.Fatalf("saved effort = %q, want %q", got.TaskReasoningEffort, want)
		}
		k := findKnob(t, knobsGET(t, srv), "task_reasoning_effort")
		if k.Raw != want || !k.NextSession {
			t.Fatalf("GET = %+v", k)
		}
		if llm.ReasoningEffort() != initial {
			t.Fatal("worker knob mutated global effort")
		}
	}
	rec := knobsPOST(t, srv, `{"key":"task_reasoning_effort","value":"invalid"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid effort accepted: %d", rec.Code)
	}
	got, _ := config.LoadToml(path)
	if got.TaskReasoningEffort != "" {
		t.Fatal("invalid value persisted")
	}
}
