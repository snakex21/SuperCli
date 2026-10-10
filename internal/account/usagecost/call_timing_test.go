package usagecost

import (
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
	"testing"
	"time"
)

func TestCallUsageKeepsMeasuredClockButExcludesIncompleteSpeedSamples(t *testing.T) {
	for _, tc := range []struct {
		name             string
		failed, canceled bool
		duration, first  time.Duration
		known            bool
	}{
		{"complete", false, false, 10 * time.Second, 2 * time.Second, true},
		{"failed", true, false, 10 * time.Second, 2 * time.Second, false},
		{"canceled", false, true, 10 * time.Second, 2 * time.Second, false},
		{"older-injected", false, false, 0, 0, false},
		{"invalid-clock", false, false, time.Second, 2 * time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stat := llm.CallStat{Duration: tc.duration, TTFT: tc.first, Failed: tc.failed, Canceled: tc.canceled, TokensIn: 100, TokensOut: 20, Purpose: "compact"}
			u := CallUsage(config.TomlConfig{}, stat, session.UsageRecord{SessionID: "s"})
			if u.HasTiming != tc.known || u.Input != 100 || u.Output != 20 || u.Source != "compact" || u.PriceSnapshot == nil {
				t.Fatalf("usage/coverage changed: %+v", u)
			}
			if tc.known && u.DurationMS != 10000 {
				t.Fatalf("clock lost: %+v", u)
			}
			if !tc.known && u.DurationMS != 0 {
				t.Fatalf("incomplete speed sample: %+v", u)
			}
		})
	}
}
