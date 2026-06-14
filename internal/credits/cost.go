package credits

import (
	"fmt"
	"strings"
	"sync"
)

// Rate is the per-model USD cost per 1k tokens. These
// rates are rough estimates kept in source for F7; a
// future F15+ build will fetch live prices.
//
// IMPORTANT: USD is NEVER stored in SQLite. The cost
// layer exists only to *display* a dollar estimate on
// request (--status, TUI status bar). Authoritative
// numbers are token counts.
type Rate struct {
	// InputPer1k is USD per 1000 input tokens.
	InputPer1k float64
	// OutputPer1k is USD per 1000 output tokens.
	OutputPer1k float64
}

// modelRates is the lookup table. Keys are matched
// case-insensitively and against the lowercased model
// id. Common aliases ("gpt-4o", "gpt-4o-2024-08-06")
// all collapse to the same entry.
//
// "default" is the fallback for unknown models.
var modelRates = map[string]Rate{
	"default": {InputPer1k: 1.00, OutputPer1k: 3.00},

	// OpenAI
	"gpt-4o-mini":       {InputPer1k: 0.15, OutputPer1k: 0.60},
	"gpt-4o":            {InputPer1k: 2.50, OutputPer1k: 10.00},
	"gpt-4.1":           {InputPer1k: 2.00, OutputPer1k: 8.00},
	"gpt-4.1-mini":      {InputPer1k: 0.40, OutputPer1k: 1.60},
	"gpt-4.1-nano":      {InputPer1k: 0.10, OutputPer1k: 0.40},
	"gpt-3.5-turbo":     {InputPer1k: 0.50, OutputPer1k: 1.50},
	"o1-mini":           {InputPer1k: 3.00, OutputPer1k: 12.00},
	"o1":                {InputPer1k: 15.00, OutputPer1k: 60.00},
	"o1-preview":        {InputPer1k: 15.00, OutputPer1k: 60.00},
	"o3-mini":           {InputPer1k: 1.10, OutputPer1k: 4.40},
	"o3":                {InputPer1k: 10.00, OutputPer1k: 40.00},
	"o4-mini":           {InputPer1k: 1.10, OutputPer1k: 4.40},
	"chatgpt-4o-latest": {InputPer1k: 5.00, OutputPer1k: 15.00},

	// OpenAI GPT-5 family (Codex backend, June 2026):
	// gpt-5.5 is the flagship, gpt-5.4-mini the cheap/fast
	// option, gpt-5.3-codex-spark the low-latency preview.
	"gpt-5.5":             {InputPer1k: 1.25, OutputPer1k: 10.00},
	"gpt-5.4-mini":        {InputPer1k: 0.25, OutputPer1k: 2.00},
	"gpt-5.3-codex-spark": {InputPer1k: 0.50, OutputPer1k: 4.00},

	// Anthropic
	"claude-3-5-haiku-latest":  {InputPer1k: 0.80, OutputPer1k: 4.00},
	"claude-3-5-sonnet-latest": {InputPer1k: 3.00, OutputPer1k: 15.00},
	"claude-haiku-4-5":         {InputPer1k: 1.00, OutputPer1k: 5.00},
	"claude-sonnet-4-5":        {InputPer1k: 3.00, OutputPer1k: 15.00},
	"claude-opus-4-1":          {InputPer1k: 15.00, OutputPer1k: 75.00},
	"claude-opus-4":            {InputPer1k: 15.00, OutputPer1k: 75.00},

	// Meta Llama (Together / Groq / OpenRouter all converge on these names)
	"llama-3.1-8b-instant":      {InputPer1k: 0.05, OutputPer1k: 0.08},
	"llama-3.1-70b-versatile":   {InputPer1k: 0.59, OutputPer1k: 0.79},
	"llama-3.3-70b-versatile":   {InputPer1k: 0.59, OutputPer1k: 0.79},
	"llama-3.2-3b-preview":      {InputPer1k: 0.06, OutputPer1k: 0.06},
	"llama-3.2-1b-preview":      {InputPer1k: 0.04, OutputPer1k: 0.04},

	// Mistral
	"mistral-large-latest":  {InputPer1k: 2.00, OutputPer1k: 6.00},
	"mistral-small-latest":  {InputPer1k: 0.20, OutputPer1k: 0.60},
	"mixtral-8x7b-instruct": {InputPer1k: 0.27, OutputPer1k: 0.27},
	"codestral-latest":      {InputPer1k: 0.30, OutputPer1k: 0.90},

	// Google
	"gemini-1.5-pro":   {InputPer1k: 1.25, OutputPer1k: 5.00},
	"gemini-1.5-flash": {InputPer1k: 0.075, OutputPer1k: 0.30},
	"gemini-2.0-flash": {InputPer1k: 0.10, OutputPer1k: 0.40},
	"gemini-2.5-pro":   {InputPer1k: 1.25, OutputPer1k: 10.00},

	// DeepSeek
	"deepseek-chat":     {InputPer1k: 0.14, OutputPer1k: 0.28},
	"deepseek-reasoner": {InputPer1k: 0.55, OutputPer1k: 2.19},

	// Qwen
	"qwen-2.5-72b-instruct": {InputPer1k: 0.40, OutputPer1k: 0.40},
	"qwen-2.5-coder-32b":   {InputPer1k: 0.20, OutputPer1k: 0.20},
}

// fetchedRates holds prices fetched from external sources
// (F28). Set via SetFetchedRates. When non-nil, these
// override the hardcoded modelRates above. Key is lowercased
// model ID; values are per-1M tokens (not per-1k).
var (
	fetchedMu    sync.RWMutex
	fetchedRates map[string]Rate // nil = not fetched
)

// SetFetchedRates replaces the fetched-price cache. rates
// maps lowercased model ID → Rate (per-1M converted to per-1k).
func SetFetchedRates(rates map[string]Rate) {
	fetchedMu.Lock()
	defer fetchedMu.Unlock()
	fetchedRates = rates
}

// GetFetchedRates returns a copy of the current fetched rates.
func GetFetchedRates() map[string]Rate {
	fetchedMu.RLock()
	defer fetchedMu.RUnlock()
	if fetchedRates == nil {
		return nil
	}
	cp := make(map[string]Rate, len(fetchedRates))
	for k, v := range fetchedRates {
		cp[k] = v
	}
	return cp
}

// looksLikeDate reports whether s is a YYYY-MM-DD
// 10-character date. The caller guarantees len >= 10
// and that s[4] == '-' and s[7] == '-'.
func looksLikeDate(s string) bool {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return false
	}
	for i, c := range s {
		if i == 4 || i == 7 {
			if c != '-' {
				return false
			}
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// stripDateSuffix removes a trailing "-YYYY-MM-DD" from
// modelID if present. Returns modelID unchanged if no
// date suffix is found or the prefix is empty after
// stripping.
func stripDateSuffix(modelID string) string {
	if len(modelID) < 11 { // need at least 1 char before the date
		return modelID
	}
	if !looksLikeDate(modelID[len(modelID)-10:]) {
		return modelID
	}
	stripped := modelID[:len(modelID)-10]
	// Trim trailing '-' left over from the date.
	for len(stripped) > 0 && stripped[len(stripped)-1] == '-' {
		stripped = stripped[:len(stripped)-1]
	}
	if stripped == "" {
		return modelID
	}
	return stripped
}

// RateFor returns the rate for a model id, or the
// "default" rate if the model is unknown. The lookup is
// case-insensitive and tolerates provider prefixes
// ("openai/gpt-4o" -> "gpt-4o") and OpenAI-style date
// suffixes ("gpt-4o-2024-08-06" -> "gpt-4o").
//
// Pass the empty string to get the default rate.
func RateFor(modelID string) (Rate, string) {
	if modelID == "" {
		return modelRates["default"], "default"
	}
	// Strip provider prefix: "openai/gpt-4o" -> "gpt-4o"
	if i := strings.LastIndexByte(modelID, '/'); i >= 0 && i < len(modelID)-1 {
		modelID = modelID[i+1:]
	}
	modelID = stripDateSuffix(modelID)
	key := strings.ToLower(strings.TrimSpace(modelID))
	// F28: check fetched rates first (external > hardcoded).
	fetchedMu.RLock()
	if fetchedRates != nil {
		if r, ok := fetchedRates[key]; ok {
			fetchedMu.RUnlock()
			return r, key + " (fetched)"
		}
	}
	fetchedMu.RUnlock()
	if r, ok := modelRates[key]; ok {
		return r, key
	}
	return modelRates["default"], "default"
}

// CostFor estimates the USD cost of a (in, out) token
// pair at the model's rate. Input/output of 0 returns 0.
// The result is rounded to 6 decimal places for stable
// display; underlying float is preserved if you need
// more precision.
func CostFor(modelID string, inputTokens, outputTokens int64) float64 {
	if inputTokens <= 0 && outputTokens <= 0 {
		return 0
	}
	rate, _ := RateFor(modelID)
	in := float64(inputTokens) / 1000.0 * rate.InputPer1k
	out := float64(outputTokens) / 1000.0 * rate.OutputPer1k
	return roundUSD(in + out)
}

func roundUSD(v float64) float64 {
	// 1e-6 cents precision is way more than USD needs
	// but it keeps floats from showing as 0.0000012
	// in the status bar.
	return float64(int64(v*1_000_000+0.5)) / 1_000_000
}

// FormatUSD renders a USD amount as "$0.00", "$0.01",
// or "$1234" depending on magnitude. Negative amounts
// get a leading "-". Trivial sub-cent amounts (below
// $0.005) round to "$0.00" so the status bar doesn't
// flicker.
func FormatUSD(v float64) string {
	if v < 0 {
		return "-" + FormatUSD(-v)
	}
	switch {
	case v < 0.005:
		return "$0.00"
	case v < 1000:
		return fmt.Sprintf("$%.2f", v)
	default:
		return fmt.Sprintf("$%.0f", v)
	}
}
