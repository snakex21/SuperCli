package prompt

import (
	"strings"
	"testing"
)

// Core must stay lightweight: every request pays for it and
// small models receive ONLY it. ~300 tokens at ~4 chars/token.
func TestCoreStaysSmall(t *testing.T) {
	const charBudget = 1200
	if len(Core) > charBudget {
		t.Errorf("Core is %d chars, budget %d (~300 tokens)", len(Core), charBudget)
	}
}

func TestBuildLayers(t *testing.T) {
	if got := Build(true); got != Core {
		t.Errorf("small tier must get Core only, got %d chars", len(got))
	}
	big := Build(false)
	if !strings.HasPrefix(big, Core) {
		t.Error("big prompt must start with Core")
	}
	if !strings.Contains(big, "file_path:line") {
		t.Error("big prompt missing extended guidance")
	}
	// No profile concept anywhere.
	if strings.Contains(strings.ToLower(big), "profile") {
		t.Error("prompt must not mention profiles")
	}
}

func TestCoreKeepsOfficeHints(t *testing.T) {
	if !strings.Contains(Core, "backup") {
		t.Error("Core must state backups are automatic")
	}
	if !strings.Contains(Core, "state which file changed") {
		t.Error("Core must require stating what file changed")
	}
}
