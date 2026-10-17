package config

import (
	"path/filepath"
	"testing"
)

func TestPurposeReasoningEffortMergeAndRoundtrip(t *testing.T) {
	dst := TomlConfig{ReasoningEffort: "medium", AdvisorReasoningEffort: "low", ReviewReasoningEffort: "high", DarwinReasoningEffort: "max"}
	mergeToml(&dst, TomlConfig{})
	if dst.AdvisorReasoningEffort != "low" || dst.ReviewReasoningEffort != "high" || dst.DarwinReasoningEffort != "max" {
		t.Fatal("empty override cleared effort")
	}
	mergeToml(&dst, TomlConfig{AdvisorReasoningEffort: "minimal", ReviewReasoningEffort: "xhigh", DarwinReasoningEffort: "none"})
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := SaveToml(path, dst); err != nil {
		t.Fatal(err)
	}
	got, err := LoadToml(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.AdvisorReasoningEffort != "minimal" || got.ReviewReasoningEffort != "xhigh" || got.DarwinReasoningEffort != "none" || got.ReasoningEffort != "medium" {
		t.Fatalf("roundtrip: %+v", got)
	}
}
