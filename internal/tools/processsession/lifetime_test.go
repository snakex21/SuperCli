package processsession

import (
	"encoding/json"
	"testing"
	"time"

	"supercli/internal/tools/ctxexec"
)

func TestSessionLifetimeWaitsUntilExitAndHonorsOptionalDeadline(t *testing.T) {
	for _, test := range []struct {
		name  string
		input int
		want  time.Duration
	}{
		{"omitted", 0, 0},
		{"one millisecond", 1, time.Millisecond},
		{"minimum", 1000, time.Second},
		{"short", 1500, 1500 * time.Millisecond},
		{"former maximum", 30 * 60 * 1000, 30 * time.Minute},
		{"installation", 2 * 60 * 60 * 1000, 2 * time.Hour},
		{"former day maximum", 24 * 60 * 60 * 1000, 24 * time.Hour},
		{"above former maximum", 24*60*60*1000 + 1, 24*time.Hour + time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got, err := sessionLifetime(test.input); err != nil || got != test.want {
				t.Fatalf("lifetime(%d)=%v, want %v err=%v", test.input, got, test.want, err)
			}
		})
	}
	for _, invalid := range []int{-1, int(^uint(0) >> 1)} {
		if invalid > 0 && int64(invalid) <= ctxexec.MaxTimeoutMS {
			continue
		}
		if _, err := sessionLifetime(invalid); err == nil {
			t.Fatalf("invalid duration accepted: %d", invalid)
		}
	}
}

func TestProcessSessionSchemaMatchesLifetimeBounds(t *testing.T) {
	var schema struct {
		Properties struct {
			Timeout struct {
				Minimum int64 `json:"minimum"`
				Maximum int64 `json:"maximum"`
				Default int64 `json:"default"`
			} `json:"timeout_ms"`
		} `json:"properties"`
	}
	tool := &Tool{}
	if err := json.Unmarshal([]byte(tool.Spec().Schema), &schema); err != nil {
		t.Fatal(err)
	}
	limits := schema.Properties.Timeout
	if limits.Minimum != 0 || limits.Maximum != ctxexec.MaxTimeoutMS || limits.Default != 0 {
		t.Fatalf("schema lifetime limits differ from execution: %+v", limits)
	}
	if lifetime, err := sessionLifetime(int(limits.Default)); err != nil || lifetime != 0 {
		t.Fatal("advertised default does not execute exactly")
	}
}
