package agent

import (
	"strings"
	"testing"
)

// The window-guard stop must tell the user what to do next.
func TestContextWindowRemedyNamesActions(t *testing.T) {
	for _, want := range []string{"larger context", "/compact", "new conversation"} {
		if !strings.Contains(contextWindowRemedy, want) {
			t.Fatalf("remedy %q does not mention %q", contextWindowRemedy, want)
		}
	}
}
