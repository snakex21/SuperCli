package app

import "testing"

func TestBuildTUIOptionsNoColor(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "NO_COLOR" {
				return v
			}
			return ""
		}
	}
	cases := []struct {
		name   string
		config bool
		envVal string
		want   bool
	}{
		{"default", false, "", false},
		{"config", true, "", true},
		{"env", false, "1", true},
		{"env any non-empty value", false, "0", true},
		{"both", true, "yes", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := buildTUIOptions(tuiLaunchDeps{noColorConfig: tc.config, getenv: env(tc.envVal)})
			if opts.NoColor != tc.want {
				t.Fatalf("NoColor = %v, want %v", opts.NoColor, tc.want)
			}
		})
	}
}

func TestTUINoColorDefaultsToProcessEnv(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if !tuiNoColor(false, nil) {
		t.Fatal("NO_COLOR from the process environment was ignored")
	}
	t.Setenv("NO_COLOR", "")
	if tuiNoColor(false, nil) {
		t.Fatal("empty NO_COLOR must not disable colour")
	}
}
