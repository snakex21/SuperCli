package prompt

import (
	"strings"
	"testing"
)

func TestMemoryGuidancePreservesUserControl(t *testing.T) {
	enabled := MemoryGuidance(true)
	for _, want := range []string{"recall only for missing prior context", "new durable facts", "user preferences", "when explicitly asked", "Never store secrets"} {
		if !strings.Contains(enabled, want) {
			t.Errorf("automatic memory guidance missing %q", want)
		}
	}
	if !strings.Contains(enabled, "Do not call memory tools just to start or finish a task") {
		t.Error("task boundary ritual returned")
	}
	disabled := MemoryGuidance(false)
	if disabled != "Do not proactively save new memory. Only call remember when the user explicitly asks you to remember something." {
		t.Fatalf("explicit memory-off policy changed: %q", disabled)
	}
}
