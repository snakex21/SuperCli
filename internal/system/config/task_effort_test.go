package config

import "testing"

func TestMergeTomlTaskReasoningEffort(t *testing.T) {
	dst := TomlConfig{TaskReasoningEffort: "high"}
	mergeToml(&dst, TomlConfig{})
	if dst.TaskReasoningEffort != "high" {
		t.Fatal("empty project override cleared effort")
	}
	mergeToml(&dst, TomlConfig{TaskReasoningEffort: "low"})
	if dst.TaskReasoningEffort != "low" {
		t.Fatal("project override was ignored")
	}
}
