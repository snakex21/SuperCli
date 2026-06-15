package planmode

import (
	"strings"
	"testing"
)

func TestWrapPrompt(t *testing.T) {
	result := WrapPrompt("fix the bug")
	if !strings.Contains(result, "fix the bug") {
		t.Error("missing original prompt")
	}
	if !strings.Contains(result, "PLAN MODE ACTIVE") {
		t.Error("missing plan mode marker")
	}
	if !strings.Contains(result, "Do NOT make any file changes") {
		t.Error("missing read-only instruction")
	}
}

func TestIsPlanPrompt_True(t *testing.T) {
	prompt := WrapPrompt("test")
	if !IsPlanPrompt(prompt) {
		t.Error("should detect plan mode prompt")
	}
}

func TestIsPlanPrompt_False(t *testing.T) {
	cases := []string{
		"fix the bug",
		"",
		"some [PLAN] text without ACTIVE",
	}
	for _, c := range cases {
		if IsPlanPrompt(c) {
			t.Errorf("IsPlanPrompt(%q) = true, want false", c)
		}
	}
}

func TestStatusLabel(t *testing.T) {
	if got := StatusLabel(true); got != "PLAN" {
		t.Errorf("StatusLabel(true) = %q, want PLAN", got)
	}
	if got := StatusLabel(false); got != "" {
		t.Errorf("StatusLabel(false) = %q, want empty", got)
	}
}

func TestSuffix_ContainsFormat(t *testing.T) {
	if !strings.Contains(Suffix, "## Plan") {
		t.Error("suffix should contain plan format")
	}
	if !strings.Contains(Suffix, "### Phase") {
		t.Error("suffix should contain phase format")
	}
	if !strings.Contains(Suffix, "- [ ]") {
		t.Error("suffix should contain checkbox format")
	}
}
