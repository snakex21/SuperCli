package webgui

import (
	"testing"

	"supercli/internal/llm"
)

// The ChatGPT backend's placeholder secondary window (0 %, no duration, no
// reset) must not reach the subscription panel as an unlabelled bar.
func TestCodexWindowInformativeHidesPlaceholder(t *testing.T) {
	zero, used, week, reset := 0.0, 12.0, int64(604800), int64(1900000000)
	cases := []struct {
		name string
		w    *llm.CodexUsageWindow
		want bool
	}{
		{"nil", nil, false},
		{"placeholder", &llm.CodexUsageWindow{UsedPercent: &zero}, false},
		{"empty", &llm.CodexUsageWindow{}, false},
		{"duration", &llm.CodexUsageWindow{UsedPercent: &zero, WindowSeconds: &week}, true},
		{"reset", &llm.CodexUsageWindow{ResetsAt: &reset}, true},
		{"used", &llm.CodexUsageWindow{UsedPercent: &used}, true},
	}
	for _, c := range cases {
		if got := codexWindowInformative(c.w); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
