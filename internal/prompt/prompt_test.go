package prompt

import (
	"strings"
	"testing"
)

// Core must stay lightweight: every request pays for it and
// weak models receive ONLY it. ~300 tokens at ~4 chars/token.
func TestCoreStaysSmall(t *testing.T) {
	const charBudget = 1200
	if len(Core) > charBudget {
		t.Errorf("Core is %d chars, budget %d (~300 tokens)", len(Core), charBudget)
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"":        "coding",
		"auto":    "coding",
		"AUTO":    "coding",
		"office":  "office",
		" Office": "office",
		"coding":  "coding",
		"bogus":   "coding",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildLayers(t *testing.T) {
	office := Build("office", false)
	if !strings.HasPrefix(office, Core) {
		t.Error("office prompt must start with Core")
	}
	if !strings.Contains(office, "not technical") {
		t.Error("office prompt missing office profile")
	}

	coding := Build("coding", false)
	if !strings.Contains(coding, "file_path:line") {
		t.Error("coding prompt missing coding profile")
	}

	// Weak model: core only, regardless of profile.
	if got := Build("office", true); got != Core {
		t.Errorf("weak model must get Core only, got %d chars", len(got))
	}
}

func TestWeakTier(t *testing.T) {
	if WeakTier(0, 0) {
		t.Error("unknown cost must not be weak")
	}
	if !WeakTier(0.1, 0.4) {
		t.Error("cheap model should be weak tier")
	}
	if WeakTier(3, 15) {
		t.Error("frontier model must not be weak tier")
	}
}
