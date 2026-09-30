package app

import (
	"strings"
	"testing"
)

func TestBuildSystemPromptMemoryIsConditional(t *testing.T) {
	got := buildSystemPrompt(nil)
	for _, forbidden := range []string{"after completing a task, call remember", "Use recall at the start of non-trivial tasks"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("unconditional memory ritual: %q", forbidden)
		}
	}
	for _, required := range []string{"missing prior context", "preference", "Never store secrets"} {
		if !strings.Contains(got, required) {
			t.Errorf("memory guidance lost %q", required)
		}
	}
}
