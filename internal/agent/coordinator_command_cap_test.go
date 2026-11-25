package agent

import (
	"encoding/json"
	"testing"
)

// Audited: coordinator commands of 321-749 s (timeout_ms 3600000) held worker
// reports back. In orchestrator mode or with workers running they are capped.
func TestCoordinatorCommandsAreCapped(t *testing.T) {
	timeout := func(raw json.RawMessage) int64 {
		var a struct {
			TimeoutMS int64 `json:"timeout_ms"`
		}
		_ = json.Unmarshal(raw, &a)
		return a.TimeoutMS
	}
	orch := &Loop{route: RouteCoordinator, orchestrator: true}
	for _, tc := range []struct {
		args string
		want int64
	}{
		{`{"command":["gradlew","build"],"timeout_ms":3600000}`, 120000},
		{`{"command":["gradlew","build"]}`, 120000},
		{`{"command":["gradlew","build"],"timeout_ms":30000}`, 30000},
	} {
		got, _ := orch.capCoordinatorCommand("ctx_execute", json.RawMessage(tc.args))
		if timeout(got) != tc.want {
			t.Fatalf("%s -> timeout %d, want %d", tc.args, timeout(got), tc.want)
		}
	}
	withWorkers := &Loop{route: RouteCoordinator}
	withWorkers.backgroundActive.Store(2)
	if got, ok := withWorkers.capCoordinatorCommand("ctx_execute", json.RawMessage(`{"command":["x"],"timeout_ms":600000}`)); !ok || timeout(got) != 120000 {
		t.Fatal("a coordinator with running workers was not capped")
	}
	worker := &Loop{route: RouteCoordinator}
	if _, ok := worker.capCoordinatorCommand("ctx_execute", json.RawMessage(`{"command":["x"],"timeout_ms":600000}`)); ok {
		t.Fatal("a worker's (or plain coordinator's) command was capped")
	}
}
