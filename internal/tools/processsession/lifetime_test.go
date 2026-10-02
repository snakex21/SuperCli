package processsession

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSessionLifetimeRequiresExplicitLongJobTimeout(t *testing.T) {
	for _, test := range []struct {
		name  string
		input int
		want  time.Duration
	}{
		{"omitted", 0, 10 * time.Minute},
		{"negative", -1, 10 * time.Minute},
		{"minimum clamp", 1, time.Second},
		{"minimum", 1000, time.Second},
		{"short", 1500, 1500 * time.Millisecond},
		{"former maximum", 30 * 60 * 1000, 30 * time.Minute},
		{"installation", 2 * 60 * 60 * 1000, 2 * time.Hour},
		{"new maximum", 24 * 60 * 60 * 1000, 24 * time.Hour},
		{"above maximum", 24*60*60*1000 + 1, 24 * time.Hour},
		{"integer overflow guard", int(^uint(0) >> 1), 24 * time.Hour},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := sessionLifetime(test.input); got != test.want {
				t.Fatalf("lifetime(%d)=%v, want %v", test.input, got, test.want)
			}
		})
	}
}

func TestProcessSessionSchemaMatchesLifetimeBounds(t *testing.T) {
	var schema struct {
		Properties struct {
			Timeout struct {
				Minimum int `json:"minimum"`
				Maximum int `json:"maximum"`
				Default int `json:"default"`
			} `json:"timeout_ms"`
		} `json:"properties"`
	}
	tool := &Tool{}
	if err := json.Unmarshal([]byte(tool.Spec().Schema), &schema); err != nil {
		t.Fatal(err)
	}
	limits := schema.Properties.Timeout
	if limits.Minimum != 1000 || limits.Maximum != int(maxLifetime/time.Millisecond) || limits.Default != int(defaultLifetime/time.Millisecond) {
		t.Fatalf("schema lifetime limits differ from execution: %+v", limits)
	}
	if sessionLifetime(limits.Default) != defaultLifetime || sessionLifetime(limits.Maximum) != maxLifetime {
		t.Fatal("advertised lifetime limits do not execute exactly")
	}
}
