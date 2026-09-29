package llm

import (
	"strings"
	"testing"
	"time"
)

func TestCodexUsageDetailLocalizesLabelsAndPreservesNumbers(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	rl := CodexRateLimits{OK: true, PrimaryUsedPct: 37, SecondaryUsedPct: 21,
		PrimaryWindowMin: 300, SecondaryWindowMin: 10080,
		PrimaryResetAt: now.Add(time.Hour).Unix(), SecondaryResetAt: now.Add(2 * time.Hour).Unix()}
	english := rl.formatDetailAt(now)
	polish := rl.formatDetailAtFor(now, "pl")
	for _, number := range []string{"5h", "7d", "37%", "21%", "1h", "2h"} {
		if !strings.Contains(english, number) || !strings.Contains(polish, number) {
			t.Fatalf("localized quota lost %s: %q / %q", number, english, polish)
		}
	}
	if !strings.Contains(english, "window") || !strings.Contains(polish, "okno") || !strings.Contains(polish, "reset za") {
		t.Fatalf("quota labels were not localized: %q / %q", english, polish)
	}
}
