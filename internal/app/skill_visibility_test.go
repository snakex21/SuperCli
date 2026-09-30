package app

import (
	"supercli/internal/tools"
	"testing"
)

func TestSkillDiscoveryVisibleInEveryExecutionProfile(t *testing.T) {
	for _, coordinator := range []bool{false, true} {
		for _, thin := range []bool{false, true} {
			registry := tools.NewRegistry()
			registry.MustRegister(tools.NewSkillApplier(tools.NewDiscoverer(t.TempDir(), t.TempDir())).Spec())
			applyAlwaysOnToolProfile(registry, coordinator, thin)
			visible := registry.VisibleNames()
			if len(visible) != 1 || visible[0] != "apply_skill" {
				t.Fatalf("skill missing: coordinator=%v thin=%v visible=%v", coordinator, thin, visible)
			}
			registry.ResetVisibility()
			if len(registry.VisibleNames()) != 1 {
				t.Fatal("skill disappeared on new turn/session")
			}
		}
	}
}
