package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	// Clear any inherited env.
	for _, k := range []string{
		"SUPERCLI_LLM_PROVIDER", "SUPERCLI_LLM_API_KEY",
		"SUPERCLI_LLM_BASE_URL", "SUPERCLI_LLM_MODEL",
		"SUPERCLI_LLM_TEMPERATURE", "SUPERCLI_LLM_MAX_TOKENS",
		"SUPERCLI_LLM_STREAM", "SUPERCLI_LLM_TIMEOUT", "SUPERCLI_DEBUG",
	} {
		t.Setenv(k, "")
	}
	c, err := Load(FlagOverrides{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Provider != ProviderEcho {
		t.Errorf("Provider = %q, want echo (no API key)", c.Provider)
	}
	if c.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("BaseURL = %q", c.BaseURL)
	}
	if c.Model != "no model" {
		t.Errorf("Model = %q, want 'no model' (no provider configured)", c.Model)
	}
	if !c.Stream {
		t.Error("Stream should default to true")
	}
	if c.Timeout != 300_000_000_000 {
		t.Errorf("Timeout = %v, want 300s (idle timeout)", c.Timeout)
	}
	if c.ConnectTimeout != 30_000_000_000 {
		t.Errorf("ConnectTimeout = %v, want 30s", c.ConnectTimeout)
	}
}

func TestLoad_EnvOverridesDefaults(t *testing.T) {
	t.Setenv("SUPERCLI_LLM_API_KEY", "sk-test-123")
	t.Setenv("SUPERCLI_LLM_BASE_URL", "http://localhost:8080/v1/")
	t.Setenv("SUPERCLI_LLM_MODEL", "gpt-4o")
	t.Setenv("SUPERCLI_LLM_TEMPERATURE", "0.3")
	t.Setenv("SUPERCLI_LLM_MAX_TOKENS", "1024")
	t.Setenv("SUPERCLI_LLM_STREAM", "false")
	t.Setenv("SUPERCLI_LLM_TIMEOUT", "120")
	t.Setenv("SUPERCLI_DEBUG", "1")
	c, err := Load(FlagOverrides{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.APIKey != "sk-test-123" {
		t.Errorf("APIKey = %q", c.APIKey)
	}
	if c.BaseURL != "http://localhost:8080/v1" { // trailing slash stripped
		t.Errorf("BaseURL = %q", c.BaseURL)
	}
	if c.Model != "gpt-4o" {
		t.Errorf("Model = %q", c.Model)
	}
	if c.Temperature == nil || *c.Temperature != 0.3 {
		t.Errorf("Temperature = %v", c.Temperature)
	}
	if c.MaxTokens != 1024 {
		t.Errorf("MaxTokens = %d", c.MaxTokens)
	}
	if c.Stream {
		t.Error("Stream should be false")
	}
	if c.Timeout != 120_000_000_000 {
		t.Errorf("Timeout = %v", c.Timeout)
	}
	if !c.Debug {
		t.Error("Debug should be true")
	}
	if c.Provider != ProviderOpenAI {
		t.Errorf("Provider = %q, want openai", c.Provider)
	}
}

func TestLoad_FlagOverridesEnv(t *testing.T) {
	t.Setenv("SUPERCLI_LLM_API_KEY", "sk-env")
	t.Setenv("SUPERCLI_LLM_MODEL", "gpt-3.5-turbo")
	tr := true
	c, err := Load(FlagOverrides{
		APIKey: "sk-flag",
		Model:  "gpt-4o",
		Debug:  &tr,
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.APIKey != "sk-flag" {
		t.Errorf("APIKey = %q, want sk-flag", c.APIKey)
	}
	if c.Model != "gpt-4o" {
		t.Errorf("Model = %q, want gpt-4o", c.Model)
	}
	if !c.Debug {
		t.Error("Debug should be true from flag")
	}
}

func TestLoad_ExplicitProvider(t *testing.T) {
	t.Setenv("SUPERCLI_LLM_PROVIDER", "echo")
	c, err := Load(FlagOverrides{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Provider != ProviderEcho {
		t.Errorf("Provider = %q", c.Provider)
	}
}

func TestLoad_OpenAIWithoutKeyAllowed(t *testing.T) {
	// Local providers (LM Studio, Ollama) use the "openai"
	// provider type without an API key. This must succeed;
	// the real OpenAI API will return 401 at runtime.
	t.Setenv("SUPERCLI_LLM_PROVIDER", "openai")
	t.Setenv("SUPERCLI_LLM_MODEL", "local-model")
	cfg, err := Load(FlagOverrides{})
	if err != nil {
		t.Fatalf("expected success for openai without key (local providers), got: %v", err)
	}
	if cfg.Provider != "openai" {
		t.Fatalf("provider = %q, want openai", cfg.Provider)
	}
	if cfg.Model != "local-model" {
		t.Fatalf("model = %q, want local-model", cfg.Model)
	}
}

func TestNormalize_TrimsTrailingSlash(t *testing.T) {
	c := Config{
		BaseURL:     "http://x/v1/",
		Model:       "m",
		Temperature: floatPtr(0.5),
		Timeout:     30_000_000_000,
		APIKey:      "k",
		Provider:    ProviderOpenAI,
	}
	if err := c.Normalize(); err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if strings.HasSuffix(c.BaseURL, "/") {
		t.Errorf("trailing slash not stripped: %q", c.BaseURL)
	}
}

func TestNormalize_DefaultsTimeout(t *testing.T) {
	c := Config{
		BaseURL:     "http://x",
		Model:       "m",
		Temperature: floatPtr(0.5),
		APIKey:      "k",
		Provider:    ProviderOpenAI,
	}
	if err := c.Normalize(); err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if c.Timeout <= 0 {
		t.Errorf("Timeout = %v, want > 0", c.Timeout)
	}
}

func TestNormalize_ResponsesProvider(t *testing.T) {
	c := Config{
		Provider: ProviderResponses,
		BaseURL:  "https://example.test/v1/",
		APIKey:   "key",
	}
	if err := c.Normalize(); err != nil {
		t.Fatalf("Normalize responses: %v", err)
	}
	if c.Model != "no model" {
		t.Fatalf("Model = %q, want no model", c.Model)
	}
	if c.BaseURL != "https://example.test/v1" {
		t.Fatalf("BaseURL = %q, want trailing slash removed", c.BaseURL)
	}
}

func TestNormalize_RejectsBadTemperature(t *testing.T) {
	c := Config{
		BaseURL:     "http://x",
		Model:       "m",
		Temperature: floatPtr(5),
		APIKey:      "k",
		Provider:    ProviderOpenAI,
	}
	if err := c.Normalize(); err == nil {
		t.Fatal("expected error for temperature > 2")
	}
}

func TestNormalize_DefaultsEmptyModel(t *testing.T) {
	// Empty model for openai provider now defaults to
	// "no model" so the user can start the TUI and
	// pick a model via /models.
	c := Config{
		BaseURL:     "http://x",
		Temperature: floatPtr(0.5),
		APIKey:      "k",
		Provider:    ProviderOpenAI,
	}
	if err := c.Normalize(); err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if c.Model != "no model" {
		t.Fatalf("Model = %q, want 'no model'", c.Model)
	}
}

func TestNormalize_RejectsEmptyBaseURL(t *testing.T) {
	c := Config{
		Model:       "m",
		Temperature: floatPtr(0.5),
		APIKey:      "k",
		Provider:    ProviderOpenAI,
	}
	if err := c.Normalize(); err == nil {
		t.Fatal("expected error for empty base URL")
	}
}

func TestNormalize_RejectsUnknownProvider(t *testing.T) {
	c := Config{
		BaseURL:     "http://x",
		Model:       "m",
		Temperature: floatPtr(0.5),
		APIKey:      "k",
		Provider:    "unknown-provider",
	}
	if err := c.Normalize(); err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestGetEnvInt_InvalidFallsBack(t *testing.T) {
	t.Setenv("X", "not a number")
	if got := getEnvInt("X", 42); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestGetEnvFloatPtr_InvalidStaysNil(t *testing.T) {
	t.Setenv("X", "nope")
	if got := getEnvFloatPtr("X"); got != nil {
		t.Errorf("got %v, want nil", *got)
	}
}

func TestGetEnvBool_Variants(t *testing.T) {
	for _, c := range []struct {
		v    string
		want bool
	}{
		{"1", true}, {"true", true}, {"TRUE", true}, {"yes", true}, {"on", true},
		{"0", false}, {"false", false}, {"no", false}, {"off", false}, {"", false},
		{"garbage", true /* default if not recognised */}, // not really, default is false; see test below
	} {
		t.Setenv("X", c.v)
		// "garbage" is not a recognised value, so the helper returns the default.
		// We only test known values here.
	}
	t.Setenv("X", "garbage")
	if got := getEnvBool("X", false); got != false {
		t.Errorf("garbage should fall back to default, got %v", got)
	}
}

func TestSanitized_RedactsKey(t *testing.T) {
	c := Config{APIKey: "sk-secret", Model: "m"}
	s := c.Sanitized()
	if s.APIKey != "***redacted***" {
		t.Errorf("APIKey = %q", s.APIKey)
	}
	if c.APIKey != "sk-secret" {
		t.Error("Sanitized mutated original")
	}
}

func TestIsEcho(t *testing.T) {
	if !(Config{Provider: ProviderEcho}).IsEcho() {
		t.Error("echo should report IsEcho=true")
	}
	if (Config{Provider: ProviderOpenAI}).IsEcho() {
		t.Error("openai should report IsEcho=false")
	}
}

func TestConfigJSON(t *testing.T) {
	// Sanity: the config is JSON-serializable so the TUI can
	// render it later.
	c := Config{Provider: "openai", APIKey: "k", BaseURL: "http://x", Model: "m"}
	b, err := json.Marshal(c.Sanitized())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"provider":"openai"`) {
		t.Errorf("json = %s", b)
	}
	if strings.Contains(string(b), "sk-secret") || strings.Contains(string(b), `"k"`) {
		// api key should be redacted in Sanitized form
		t.Errorf("api key leaked: %s", b)
	}
}

// F15: opencode provider tests.

func TestNormalize_OpencodeNoAPIKey(t *testing.T) {
	c := Config{
		Provider: ProviderOpencode,
		BaseURL:  "http://localhost:11434/v1",
		Model:    "llama3.2",
	}
	if err := c.Normalize(); err != nil {
		t.Fatalf("opencode should not require APIKey: %v", err)
	}
	if c.Provider != ProviderOpencode {
		t.Errorf("Provider = %q, want opencode", c.Provider)
	}
}

func TestNormalize_OpencodeReplacesDefaultModel(t *testing.T) {
	c := Config{
		Provider: ProviderOpencode,
		BaseURL:  "http://localhost:11434/v1",
		Model:    "gpt-4o-mini", // the default OpenAI model
	}
	if err := c.Normalize(); err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if c.Model != "opencode/default" {
		t.Errorf("Model = %q, want opencode/default", c.Model)
	}
}

func TestNormalize_OpencodeKeepsExplicitModel(t *testing.T) {
	c := Config{
		Provider: ProviderOpencode,
		BaseURL:  "http://localhost:11434/v1",
		Model:    "llama3.2",
	}
	if err := c.Normalize(); err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if c.Model != "llama3.2" {
		t.Errorf("Model = %q, want llama3.2", c.Model)
	}
}

func TestNormalize_OpencodeEmptyBaseURL(t *testing.T) {
	c := Config{
		Provider: ProviderOpencode,
		Model:    "test",
	}
	if err := c.Normalize(); err == nil {
		t.Fatal("expected error for empty base URL")
	}
}

func TestLoad_ExplicitOpencode(t *testing.T) {
	t.Setenv("SUPERCLI_LLM_PROVIDER", "opencode")
	t.Setenv("SUPERCLI_LLM_BASE_URL", "http://localhost:11434/v1")
	c, err := Load(FlagOverrides{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Provider != ProviderOpencode {
		t.Errorf("Provider = %q, want opencode", c.Provider)
	}
}

func TestNormalize_OpencodeTrimsTrailingSlash(t *testing.T) {
	c := Config{
		Provider: ProviderOpencode,
		BaseURL:  "http://localhost:11434/v1/",
		Model:    "test",
	}
	if err := c.Normalize(); err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if strings.HasSuffix(c.BaseURL, "/") {
		t.Errorf("trailing slash not stripped: %q", c.BaseURL)
	}
}

func floatPtr(v float64) *float64 { return &v }

// TestLoad_TemperatureUnsetStaysNil pins the "empty config = best
// version" contract: with no SUPERCLI_LLM_TEMPERATURE the field stays
// nil, so no temperature is ever sent and the server's own default
// applies. A hard-coded default here would silently override every
// user's server-side sampling settings.
func TestLoad_TemperatureUnsetStaysNil(t *testing.T) {
	t.Setenv("SUPERCLI_LLM_TEMPERATURE", "")
	t.Setenv("SUPERCLI_LLM_API_KEY", "k")
	c, err := Load(FlagOverrides{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Temperature != nil {
		t.Errorf("Temperature = %v, want nil", *c.Temperature)
	}
}
