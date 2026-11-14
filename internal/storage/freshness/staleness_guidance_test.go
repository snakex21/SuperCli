package freshness

import (
	"strings"
	"testing"
	"time"
)

func TestStalenessGuidanceIsDateFree(t *testing.T) {
	g := StalenessGuidance()
	if strings.Contains(g, "current_date") || strings.Contains(g, time.Now().UTC().Format("2006-")) {
		t.Fatalf("stable guidance must not carry a date: %q", g)
	}
	if !strings.HasSuffix(PromptSection(time.Now()), g) {
		t.Fatal("PromptSection must keep the same guidance after its date line")
	}
}
