package agent

import (
	"slices"
	"strings"
	"testing"
)

func TestBuiltinWorkersExposeReadMany(t *testing.T) {
	for _, worker := range BuiltinSubAgents() {
		if worker.Name == "general" { // empty means inherit the full registry
			continue
		}
		if !slices.Contains(worker.AllowedTools, "read_many") {
			t.Errorf("worker %q cannot batch file reads: %v", worker.Name, worker.AllowedTools)
		}
	}
}

func TestBuiltinWorkerPromptsTeachBatching(t *testing.T) {
	for _, worker := range BuiltinSubAgents() {
		if !strings.Contains(worker.System, "read_many") && !strings.Contains(worker.System, "Batch independent") {
			t.Errorf("worker %q prompt lacks batching guidance: %q", worker.Name, worker.System)
		}
	}
}
