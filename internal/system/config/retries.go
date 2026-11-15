package config

import (
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"

	"supercli/internal/llm"
)

// ParseMaxRetries interprets a max_retries value from config.toml or from a
// settings editor. nil and "" mean the default (5); a non-negative integer is
// a finite retry count (0 = never retry); -1, "-1", "inf", "infinite", "∞"
// and "unlimited" mean retry until success or a user cancel. Anything else
// (e.g. -5, "abc", 2.5) is an error and the caller falls back to the default.
func ParseMaxRetries(v any) (int, error) {
	switch x := v.(type) {
	case nil:
		return llm.DefaultMaxRetries, nil
	case int:
		return checkRetryCount(int64(x), v)
	case int64:
		return checkRetryCount(x, v)
	case float64:
		if x == math.Trunc(x) && !math.IsInf(x, 0) && math.Abs(x) < 1e9 {
			return checkRetryCount(int64(x), v)
		}
	case string:
		s := strings.ToLower(strings.TrimSpace(x))
		switch s {
		case "", "default":
			return llm.DefaultMaxRetries, nil
		case "inf", "infinite", "infinity", "unlimited", "∞":
			return llm.UnlimitedRetries, nil
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return checkRetryCount(n, v)
		}
	}
	return llm.DefaultMaxRetries, fmt.Errorf("max_retries = %v: want a number >= 0, -1 or \"inf\"", v)
}

func checkRetryCount(n int64, raw any) (int, error) {
	if n == int64(llm.UnlimitedRetries) {
		return llm.UnlimitedRetries, nil
	}
	if n < 0 || n > math.MaxInt32 {
		return llm.DefaultMaxRetries, fmt.Errorf("max_retries = %v: want a number >= 0, -1 or \"inf\"", raw)
	}
	return int(n), nil
}

// MaxRetriesSetting is the effective max_retries (default 5, -1 = unlimited).
// Invalid values yield the default; ApplyLLMGlobals logs the warning.
func (t TomlConfig) MaxRetriesSetting() int {
	n, _ := ParseMaxRetries(t.MaxRetries)
	return n
}

// MaxRetriesTOMLValue converts an accepted editor value into what SaveToml
// writes: nil for the default (the key is omitted) and an integer otherwise,
// so "inf" is persisted in its canonical -1 form.
func MaxRetriesTOMLValue(text string) (any, error) {
	if s := strings.ToLower(strings.TrimSpace(text)); s == "" || s == "default" {
		return nil, nil
	}
	n, err := ParseMaxRetries(text)
	if err != nil {
		return nil, err
	}
	return int64(n), nil
}

// RetryUnknownErrorsOn reports retry_unknown_errors (nil = off).
func (t TomlConfig) RetryUnknownErrorsOn() bool {
	return t.RetryUnknownErrors != nil && *t.RetryUnknownErrors
}

// ApplyRetryGlobals installs max_retries, retry_unknown_errors and the
// requests_per_minute limits (global + per [[providers]] entry). Part of
// ApplyLLMGlobals; the TUI and web GUI settings call it again after a save so
// a change applies to the next request without a restart.
func ApplyRetryGlobals(t TomlConfig) {
	n, err := ParseMaxRetries(t.MaxRetries)
	if err != nil {
		log.Printf("config: %v; using the default %d", err, llm.DefaultMaxRetries)
	}
	llm.SetMaxRetries(n)
	llm.SetRetryUnknownErrors(t.RetryUnknownErrorsOn())
	llm.ResetRequestLimits()
	rpm := t.RequestsPerMinute
	if rpm < 0 {
		log.Printf("config: requests_per_minute = %d is negative; limiter off", rpm)
		rpm = 0
	}
	llm.SetDefaultRequestsPerMinute(rpm)
	for _, p := range t.Providers {
		if p.RequestsPerMinute <= 0 {
			continue
		}
		if strings.TrimSpace(p.BaseURL) == "" {
			log.Printf("config: provider %q: requests_per_minute needs base_url; ignored", p.Name)
			continue
		}
		llm.SetRequestsPerMinute(p.BaseURL, p.RequestsPerMinute)
	}
}
