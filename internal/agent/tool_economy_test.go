package agent

import (
	"slices"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestToolEconomyProgressWarnsWithoutStoppingNovelDiscovery(t *testing.T) {
	var p toolEconomyProgress
	for i := 0; i < serialDiscoveryWarnAfter-1; i++ {
		calls := []llm.ToolCall{{Name: "read_lines", Arguments: `{"path":"f.go"}`}}
		if p.observe(calls, allOK(1)) {
			t.Fatalf("warned after only %d serial discovery rounds", i+1)
		}
	}
	if !p.observe([]llm.ToolCall{{Name: "search_code", Arguments: `{"query":"x"}`}}, allOK(1)) {
		t.Fatal("expected batching guidance after serial read-only rounds")
	}
	// This is guidance only. Novel discovery remains legal and the state has
	// no abort signal or step budget.
	if p.warnings != 1 {
		t.Fatalf("warnings=%d want 1", p.warnings)
	}
}

func TestToolEconomyProgressRecognizesBatchedWork(t *testing.T) {
	var p toolEconomyProgress
	p.serialDiscoveryRounds = serialDiscoveryWarnAfter - 1
	if p.observe([]llm.ToolCall{{Name: "read_many", Arguments: `{}`}}, allOK(1)) {
		t.Fatal("read_many must count as an efficient batch")
	}
	if p.serialDiscoveryRounds != 0 {
		t.Fatalf("read_many did not reset streak: %d", p.serialDiscoveryRounds)
	}

	p.serialDiscoveryRounds = serialDiscoveryWarnAfter - 1
	batch := []llm.ToolCall{
		{Name: "read_lines", Arguments: `{"path":"a"}`},
		{Name: "read_lines", Arguments: `{"path":"b"}`},
		{Name: "search_code", Arguments: `{"query":"x"}`},
	}
	if p.observe(batch, allOK(3)) {
		t.Fatal("three independent reads in one round must not warn")
	}
	if p.serialDiscoveryRounds != 0 {
		t.Fatalf("parallel batch did not reset streak: %d", p.serialDiscoveryRounds)
	}
}

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
