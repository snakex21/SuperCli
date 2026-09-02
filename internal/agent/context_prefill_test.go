package agent

import (
	"testing"
	"time"

	"supercli/internal/llm"
)

func TestEffectiveCompactThresholdUsesMeasuredProfileNotEndpointKind(t *testing.T) {
	profiles := llm.LoadPrefillProfiles(t.TempDir())
	profiles.Observe("user-http-connection", "m", llm.PrefillSample{
		InputTokens: 40_000,
		TTFT:        30 * time.Second,
	})
	l := &Loop{
		modelID:         "m",
		contextProvider: "user-http-connection",
		prefillProfiles: profiles,
	}
	threshold, source := l.effectiveCompactThreshold(100_000, "catalog")
	if threshold != 20_000 || source != "prefill-profile" {
		t.Fatalf("threshold=%d source=%q, want learned 20k", threshold, source)
	}
	threshold, source = l.effectiveCompactThreshold(10_000, "config")
	if threshold != 10_000 || source != "config" {
		t.Fatalf("profile must never raise hard threshold: %d/%s", threshold, source)
	}
}
