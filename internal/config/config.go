// Package config loads SuperCli's runtime configuration from
// environment variables and command-line flags. The agent loop
// and main wiring use Config to pick a provider, point it at the
// right base URL, and supply the API key.
//
// Environment variables (all optional, but SUPERCLI_LLM_PROVIDER
// or SUPERCLI_LLM_API_KEY is required for the real provider):
//
//	SUPERCLI_LLM_PROVIDER  - "openai", "echo", or "opencode" (F15)
//	SUPERCLI_LLM_API_KEY   - bearer token; empty = echo mode
//	SUPERCLI_LLM_BASE_URL  - defaults to https://api.openai.com
//	SUPERCLI_LLM_MODEL     - model name; default "gpt-4o-mini"
//	SUPERCLI_LLM_TEMPERATURE - float; default 0.7
//	SUPERCLI_LLM_MAX_TOKENS  - int; 0 = provider default
//	SUPERCLI_LLM_STREAM    - "1" or "true" to force streaming (default true)
//	SUPERCLI_LLM_TIMEOUT   - HTTP timeout in seconds; default 60
//	SUPERCLI_DEBUG         - "1" or "true" for verbose logging
//
// Flags (--key, --model, ...) override the env values. main.go
// hands the flag values into Load().
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Provider names.
const (
	ProviderOpenAI   = "openai"
	ProviderEcho     = "echo"     // explicit echo; usually inferred from empty API key
	ProviderOpencode = "opencode" // F15: opencode headless gateway (Ollama/OpenRouter/Groq)
)

// Config is the resolved runtime configuration. All fields are
// populated by Load; never construct Config directly outside tests.
type Config struct {
	// Provider selects which implementation backs the agent.
	// "openai" or "echo". Empty means "decide from API key".
	Provider string `json:"provider"`

	// APIKey is the bearer token. Empty means echo mode.
	APIKey string `json:"api_key,omitempty"`

	// BaseURL is the chat-completions endpoint root, no trailing slash.
	BaseURL string `json:"base_url"`

	// Model is the model name sent in each request.
	Model string `json:"model"`

	// Temperature, MaxTokens, Stream, Timeout, Debug are passed
	// to the provider; see env docs above for defaults.
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream"`
	Timeout     time.Duration `json:"timeout_ns"`
	Debug       bool          `json:"debug"`
}

// FlagOverrides mirrors the --key/--model/... flags. Empty fields
// are ignored. main.go builds this from cobra/pflag.
type FlagOverrides struct {
	Provider string
	APIKey   string
	BaseURL  string
	Model    string
	Debug    *bool // pointer so unset != false
}

// Load reads env vars then applies flag overrides on top.
func Load(flags FlagOverrides) (Config, error) {
	c := Config{
		Provider:    getEnv("SUPERCLI_LLM_PROVIDER", ""),
		APIKey:      getEnv("SUPERCLI_LLM_API_KEY", ""),
		BaseURL:     getEnv("SUPERCLI_LLM_BASE_URL", "https://api.openai.com/v1"),
		Model:       getEnv("SUPERCLI_LLM_MODEL", ""),
		Temperature: getEnvFloat("SUPERCLI_LLM_TEMPERATURE", 0.7),
		MaxTokens:   getEnvInt("SUPERCLI_LLM_MAX_TOKENS", 0),
		Stream:      getEnvBool("SUPERCLI_LLM_STREAM", true),
		Timeout:     time.Duration(getEnvInt("SUPERCLI_LLM_TIMEOUT", 60)) * time.Second,
		Debug:       getEnvBool("SUPERCLI_DEBUG", false),
	}

	if flags.Provider != "" {
		c.Provider = flags.Provider
	}
	if flags.APIKey != "" {
		c.APIKey = flags.APIKey
	}
	if flags.BaseURL != "" {
		c.BaseURL = flags.BaseURL
	}
	if flags.Model != "" {
		c.Model = flags.Model
	}
	if flags.Debug != nil {
		c.Debug = *flags.Debug
	}

	if err := c.Normalize(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Normalize validates the config, fills derived fields, and
// returns an error if anything is inconsistent. Call before
// handing Config to a provider or the agent loop.
func (c *Config) Normalize() error {
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	if c.BaseURL == "" {
		return fmt.Errorf("config: base URL is empty")
	}
	if c.Temperature < 0 || c.Temperature > 2 {
		return fmt.Errorf("config: temperature %v out of [0,2]", c.Temperature)
	}
	if c.Timeout <= 0 {
		c.Timeout = 60 * time.Second
	}
	if c.Provider == "" {
		if c.APIKey == "" {
			c.Provider = ProviderEcho
		} else {
			c.Provider = ProviderOpenAI
		}
	}
	switch c.Provider {
	case ProviderOpenAI:
		// Allow empty API key for local providers (LM Studio,
		// Ollama) that use the OpenAI-compatible protocol without
		// authentication. The real OpenAI API will return 401 at
		// runtime if the key is missing — that is a better UX
		// than refusing to start.
		// Model is also optional at startup — user picks one
		// via /models in the TUI.
		if c.Model == "" {
			c.Model = "no model"
		}
	case ProviderEcho:
		if c.Model == "" {
			c.Model = "no model"
		}
	case ProviderOpencode:
		// Opencode discovers models from /v1/models; a
		// placeholder is fine when no model set yet.
		// Also replace the legacy gpt-4o-mini default
		// for backwards compatibility.
		if c.Model == "" || c.Model == "gpt-4o-mini" {
			c.Model = "opencode/default"
		}
	default:
		return fmt.Errorf("config: unknown provider %q (want %q, %q, or %q)",
			c.Provider, ProviderOpenAI, ProviderEcho, ProviderOpencode)
	}
	return nil
}

// IsEcho reports whether the agent should use the echo provider.
func (c Config) IsEcho() bool { return c.Provider == ProviderEcho }

// Sanitized returns a copy of c with the API key redacted, safe
// for logging and panic dumps.
func (c Config) Sanitized() Config {
	if c.APIKey != "" {
		c.APIKey = "***redacted***"
	}
	return c
}

// helpers ------------------------------------------------------------------

func getEnv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			return n
		}
	}
	return def
}

func getEnvFloat(key string, def float64) float64 {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err == nil {
			return f
		}
	}
	return def
}

func getEnvBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "y", "on":
			return true
		case "0", "false", "no", "n", "off":
			return false
		}
	}
	return def
}
