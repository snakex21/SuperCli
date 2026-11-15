package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestParseMaxRetries(t *testing.T) {
	for _, tc := range []struct {
		in      any
		want    int
		invalid bool
	}{
		{nil, 5, false},
		{"", 5, false},
		{int64(0), 0, false},
		{int64(12), 12, false},
		{int64(-1), -1, false},
		{"inf", -1, false},
		{"INFINITE", -1, false},
		{"∞", -1, false},
		{"-1", -1, false},
		{" 7 ", 7, false},
		{float64(3), 3, false},
		{int64(-5), 5, true},
		{"abc", 5, true},
		{"-5", 5, true},
		{float64(2.5), 5, true},
		{true, 5, true},
	} {
		got, err := ParseMaxRetries(tc.in)
		if got != tc.want || (err != nil) != tc.invalid {
			t.Errorf("ParseMaxRetries(%#v) = %d, %v; want %d invalid=%v", tc.in, got, err, tc.want, tc.invalid)
		}
	}
}

func TestMaxRetriesFromTomlFileAndApply(t *testing.T) {
	t.Cleanup(func() { ApplyRetryGlobals(TomlConfig{}) })
	dir := t.TempDir()
	for _, tc := range []struct {
		line string
		want int
	}{
		{"", 5},
		{"max_retries = 9", 9},
		{"max_retries = 0", 0},
		{"max_retries = -1", -1},
		{`max_retries = "inf"`, -1},
		{`max_retries = "abc"`, 5},
		{"max_retries = -5", 5},
	} {
		path := filepath.Join(dir, "config.toml")
		if err := os.WriteFile(path, []byte(tc.line+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadToml(path)
		if err != nil {
			t.Fatalf("%q: load must not fail on a bad value: %v", tc.line, err)
		}
		if got := cfg.MaxRetriesSetting(); got != tc.want {
			t.Errorf("%q: setting = %d, want %d", tc.line, got, tc.want)
		}
		ApplyLLMGlobals(cfg, nil)
		if got := llm.MaxRetries(); got != tc.want {
			t.Errorf("%q: applied = %d, want %d", tc.line, got, tc.want)
		}
	}
}

func TestRetrySettingsRoundTripAndMerge(t *testing.T) {
	t.Cleanup(func() { ApplyRetryGlobals(TomlConfig{}) })
	v, err := MaxRetriesTOMLValue("inf")
	if err != nil || v != int64(-1) {
		t.Fatalf("inf -> %v %v", v, err)
	}
	if v, err := MaxRetriesTOMLValue(""); v != nil || err != nil {
		t.Fatalf("empty -> %v %v", v, err)
	}
	if _, err := MaxRetriesTOMLValue("-3"); err == nil {
		t.Fatal("-3 must be rejected by the editor")
	}
	on := true
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := TomlConfig{MaxRetries: int64(-1), RetryUnknownErrors: &on, RequestsPerMinute: 30,
		Providers: []ProviderConf{{Name: "cf", Type: "openai", BaseURL: "https://gw.example/v1", RequestsPerMinute: 4}}}
	if err := SaveToml(path, cfg); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	for _, want := range []string{"max_retries = -1", "retry_unknown_errors = true", "requests_per_minute = 30", "requests_per_minute = 4"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("saved config lacks %q:\n%s", want, data)
		}
	}
	loaded, err := LoadToml(path)
	if err != nil || loaded.MaxRetriesSetting() != -1 || !loaded.RetryUnknownErrorsOn() {
		t.Fatalf("round trip: %+v %v", loaded.MaxRetries, err)
	}
	// Project config overrides the global value; an absent key keeps it.
	dst := TomlConfig{MaxRetries: int64(3)}
	mergeToml(&dst, TomlConfig{})
	if dst.MaxRetriesSetting() != 3 {
		t.Fatal("absent project key must keep the global value")
	}
	mergeToml(&dst, TomlConfig{MaxRetries: "inf"})
	if dst.MaxRetriesSetting() != -1 {
		t.Fatal("project value must win")
	}
	ApplyRetryGlobals(loaded)
	if !llm.RetryUnknownErrors() || llm.RequestsPerMinuteFor("https://gw.example") != 4 || llm.RequestsPerMinuteFor("https://other.example") != 30 {
		t.Fatal("retry/limiter globals not applied")
	}
	// The default config leaves no key behind.
	if err := SaveToml(path, TomlConfig{}); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if strings.Contains(string(data), "max_retries") || strings.Contains(string(data), "requests_per_minute") || strings.Contains(string(data), "retry_unknown_errors") {
		t.Fatalf("defaults must be omitted:\n%s", data)
	}
}
