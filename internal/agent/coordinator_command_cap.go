package agent

import (
	"encoding/json"
)

// Coordinator foreground commands (after 1.0.40). The audited coordinator ran
// ctx_execute jobs of 321-749 s with timeout_ms 3600000 while its workers ran:
// worker reports waited until the command ended. A coordinator in
// orchestrator mode, or one with background workers running, runs a command
// for at most coordinatorCommandCapMS; longer work belongs in a worker or
// process_session (the timeout message says so).
const coordinatorCommandCapMS = 120_000

// capCoordinatorCommand returns ctx_execute arguments whose timeout_ms is at
// most the cap, when the cap applies. Workers are never capped.
func (l *Loop) capCoordinatorCommand(name string, raw json.RawMessage) (json.RawMessage, bool) {
	if name != "ctx_execute" || l.route != RouteCoordinator || !(l.orchestrator || l.backgroundActive.Load() > 0) {
		return raw, false
	}
	var args map[string]json.RawMessage
	if json.Unmarshal(raw, &args) != nil {
		return raw, false
	}
	var timeout int64
	if v, ok := args["timeout_ms"]; ok {
		_ = json.Unmarshal(v, &timeout)
	}
	if timeout > 0 && timeout <= coordinatorCommandCapMS {
		return raw, false
	}
	args["timeout_ms"] = json.RawMessage("120000")
	out, err := json.Marshal(args)
	if err != nil {
		return raw, false
	}
	return out, true
}
