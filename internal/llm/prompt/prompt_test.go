package prompt

import (
	"strings"
	"testing"
)

// Core must stay lightweight: every request pays for it and
// small models receive ONLY it. ~330 tokens at ~4 chars/token (1.0.27 added
// the edit_files pointer, the leftovers search and do-the-rest-and-report).
func TestCoreStaysSmall(t *testing.T) {
	const charBudget = 1320
	if len(Core) > charBudget {
		t.Errorf("Core is %d chars, budget %d (~330 tokens)", len(Core), charBudget)
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

// Turn-economy soft budget: the core must carry the proportionality
// discipline (small task = few edits; a no-op is a valid answer; no
// formatting-only edits) — and still fit the char budget above.
func TestCoreSoftBudgetRules(t *testing.T) {
	for _, want := range []string{"Match effort to scope", "no-op is a valid result", "formatting-only"} {
		if !strings.Contains(Core, want) {
			t.Errorf("Core missing soft-budget rule %q", want)
		}
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

func TestCoreWordContractIsCompactAndDeterministic(t *testing.T) {
	for _, want := range []string{
		"NEW = exactly one edit_docx(create)",
		"EXISTING = read_docx once",
		"Success means stop tools and answer",
		"Never script or unpack DOCX",
	} {
		if !strings.Contains(Core, want) {
			t.Errorf("Core missing Word contract %q", want)
		}
	}
}

// A missing target must never be redirected: in the eval a model edited a
// different file than the (nonexistent) one named and reported success. It
// must not sink the rest of the request either (1.0.27): the doable parts
// are done and the missing one is reported.
func TestCoreMissingTargetHonesty(t *testing.T) {
	for _, want := range []string{"Missing file/target", "do every other part", "say what is missing and ask", "never invent or substitute"} {
		if !strings.Contains(Core, want) {
			t.Errorf("Core missing honesty rule %q", want)
		}
	}
}
