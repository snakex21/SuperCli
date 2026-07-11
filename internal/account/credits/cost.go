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
	// CachedInputPer1k is USD per 1000 cached input tokens. Zero means
	// the source did not publish a distinct cache-read rate; callers then
	// conservatively price cached input at the normal input rate.
	CachedInputPer1k float64
	// OutputPer1k is USD per 1000 output tokens.
	OutputPer1k float64
}

// perMillion keeps the source table readable in the units vendors publish
// while Rate and the existing cost API remain per-1k internally.
func perMillion(input, cachedInput, output float64) Rate {
	return Rate{
		InputPer1k:       input / 1000,
		CachedInputPer1k: cachedInput / 1000,
		OutputPer1k:      output / 1000,
	}
}

// modelRates is the lookup table. Keys are matched
// case-insensitively and against the lowercased model
// id. Common aliases ("gpt-4o", "gpt-4o-2024-08-06")
// all collapse to the same entry.
//
// "default" is the fallback for unknown models.
var modelRates = map[string]Rate{
	"default": perMillion(1.00, 0, 3.00),

	// OpenAI
	"gpt-4o-mini":       perMillion(0.15, 0, 0.60),
	"gpt-4o":            perMillion(2.50, 0, 10.00),
	"gpt-4.1":           perMillion(2.00, 0, 8.00),
	"gpt-4.1-mini":      perMillion(0.40, 0, 1.60),
	"gpt-4.1-nano":      perMillion(0.10, 0, 0.40),
	"gpt-3.5-turbo":     perMillion(0.50, 0, 1.50),
	"o1-mini":           perMillion(3.00, 0, 12.00),
	"o1":                perMillion(15.00, 0, 60.00),
	"o1-preview":        perMillion(15.00, 0, 60.00),
	"o3-mini":           perMillion(1.10, 0, 4.40),
	"o3":                perMillion(10.00, 0, 40.00),
	"o4-mini":           perMillion(1.10, 0, 4.40),
	"chatgpt-4o-latest": perMillion(5.00, 0, 15.00),

	// OpenAI standard short-context API prices (July 2026). Codex OAuth
	// is classified as subscription usage before these rates are consulted.
	"gpt-5.6-sol":         perMillion(5.00, 0.50, 30.00),
	"gpt-5.6-terra":       perMillion(2.50, 0.25, 15.00),
	"gpt-5.6-luna":        perMillion(1.00, 0.10, 6.00),
	"gpt-5.5":             perMillion(5.00, 0.50, 30.00),
	"gpt-5.4-mini":        perMillion(0.75, 0.075, 4.50),
	"gpt-5.3-codex":       perMillion(1.75, 0.175, 14.00),
	"gpt-5.3-codex-spark": perMillion(1.75, 0.175, 14.00),

	// Anthropic
	"claude-3-5-haiku-latest":  perMillion(0.80, 0, 4.00),
	"claude-3-5-sonnet-latest": perMillion(3.00, 0, 15.00),
	"claude-haiku-4-5":         perMillion(1.00, 0, 5.00),
	"claude-sonnet-4-5":        perMillion(3.00, 0, 15.00),
	"claude-opus-4-1":          perMillion(15.00, 0, 75.00),
	"claude-opus-4":            perMillion(15.00, 0, 75.00),

	// Meta Llama (Together / Groq / OpenRouter all converge on these names)
	"llama-3.1-8b-instant":    perMillion(0.05, 0, 0.08),
	"llama-3.1-70b-versatile": perMillion(0.59, 0, 0.79),
	"llama-3.3-70b-versatile": perMillion(0.59, 0, 0.79),
	"llama-3.2-3b-preview":    perMillion(0.06, 0, 0.06),
	"llama-3.2-1b-preview":    perMillion(0.04, 0, 0.04),

	// Mistral
	"mistral-large-latest":  perMillion(2.00, 0, 6.00),
	"mistral-small-latest":  perMillion(0.20, 0, 0.60),
	"mixtral-8x7b-instruct": perMillion(0.27, 0, 0.27),
	"codestral-latest":      perMillion(0.30, 0, 0.90),

	// Google
	"gemini-1.5-pro":   perMillion(1.25, 0, 5.00),
	"gemini-1.5-flash": perMillion(0.075, 0, 0.30),
	"gemini-2.0-flash": perMillion(0.10, 0, 0.40),
	"gemini-2.5-pro":   perMillion(1.25, 0, 10.00),

	// DeepSeek
	"deepseek-v4-flash": perMillion(0.14, 0.0028, 0.28),
	"deepseek-v4-pro":   perMillion(0.435, 0.003625, 0.87),
	"deepseek-chat":     perMillion(0.14, 0.0028, 0.28),
	"deepseek-reasoner": perMillion(0.14, 0.0028, 0.28),

	// Qwen
	"qwen-2.5-72b-instruct": perMillion(0.40, 0, 0.40),
	"qwen-2.5-coder-32b":    perMillion(0.20, 0, 0.20),
}

// fetchedRates holds prices fetched from external sources
// (F28). Set via SetFetchedRates. When non-nil, these
// override the hardcoded modelRates above. Key is lowercased
// model ID; Rate values are always USD per 1k tokens.
var (
	fetchedMu    sync.RWMutex
	fetchedRates map[string]Rate // nil = not fetched
)

// providerRates holds per-proxy / per-endpoint price overrides.
// The same model id can cost differently when reached through a
// proxy with its own price list, so these are keyed by the
// "provider/model" pair (both lowercased). When a request's
// provider has an entry for the model, it wins over fetchedRates
// and modelRates alike — it is the highest-priority source,
// intended to be injected from per-endpoint config. Guarded by
// fetchedMu (same lock as fetchedRates; they are read together).
//
// OpenRouter pricing wires this with keys like
// "openrouter/anthropic/claude-3.5-sonnet" so the same model can be
// costed differently when used through a proxy/router endpoint.
var providerRates map[string]Rate // nil = no overrides

// SetFetchedRates replaces the fetched-price cache. rates
// maps lowercased model ID → Rate (per-1M converted to per-1k).
func SetFetchedRates(rates map[string]Rate) {
	fetchedMu.Lock()
	defer fetchedMu.Unlock()
	fetchedRates = rates
}

// SetProviderRates replaces the per-provider price overrides.
// Keys are "provider/model" pairs; callers may pass mixed-case
// keys — they are normalised to lowercase here so lookups match
// regardless of how the config spelled them. Pass nil to clear.
func SetProviderRates(rates map[string]Rate) {
	fetchedMu.Lock()
	defer fetchedMu.Unlock()
	if rates == nil {
		providerRates = nil
		return
	}
	norm := make(map[string]Rate, len(rates))
	for k, v := range rates {
		norm[strings.ToLower(strings.TrimSpace(k))] = v
	}
	providerRates = norm
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

func stripDisplaySuffix(modelID string) string {
	low := strings.ToLower(strings.TrimSpace(modelID))
	if strings.HasSuffix(low, " accounts)") {
		if i := strings.LastIndex(modelID, " ("); i > 0 {
			return modelID[:i]
		}
	}
	return modelID
}

// RateFor returns the rate for a model id, or the
// "default" rate if the model is unknown. The lookup is
// case-insensitive and tolerates provider prefixes
// ("openai/gpt-4o" -> "gpt-4o") and OpenAI-style date
// suffixes ("gpt-4o-2024-08-06" -> "gpt-4o").
//
// Pass the empty string to get the default rate.
func RateFor(modelID string) (Rate, string) {
	return RateForProvider("", modelID)
}

// RateForProvider is the backwards-compatible lookup used by budget/status
// callers. Unknown models still receive the legacy default. New billing UI
// must use LookupRateForProvider so "unknown" is not confused with a quote.
func RateForProvider(provider, modelID string) (Rate, string) {
	if rate, source, ok := LookupRateForProvider(provider, modelID); ok {
		return rate, source
	}
	return modelRates["default"], "default"
}

// LookupRateForProvider resolves only an exact provider/model, fetched, or
// hardcoded rate. It never fabricates a default for an unknown model.
//
// When the provider has a per-endpoint override for the model (set via
// SetProviderRates), that rate wins over the fetched and hardcoded
// tables — so the same model reached through a proxy with its own
// price list is costed correctly. provider may be empty, in which
// case this behaves exactly like RateFor.
//
// Note: the provider override is matched on the FULL model id as
// configured (e.g. "myproxy/gpt-4o"), before the provider prefix
// and date suffix are stripped for the fallback tables — so a
// proxy can price a specific alias without colliding with the canonical model.
func LookupRateForProvider(provider, modelID string) (Rate, string, bool) {
	modelID = stripDisplaySuffix(modelID)
	if modelID == "" {
		return Rate{}, "", false
	}
	fullKey := strings.ToLower(strings.TrimSpace(modelID))
	// Per-endpoint override: highest priority, before any stripping
	// so the configured "provider/model" key matches verbatim.
	providerKey := strings.ToLower(strings.TrimSpace(provider))
	if providerKey != "" {
		pkey := providerKey + "/" + fullKey
		fetchedMu.RLock()
		if providerRates != nil {
			if r, ok := providerRates[pkey]; ok {
				fetchedMu.RUnlock()
				return r, pkey + " (endpoint)", true
			}
		}
		fetchedMu.RUnlock()
	}
	// Fetched rates may legitimately include provider prefixes from
	// OpenRouter (e.g. "anthropic/claude-3.5-sonnet"). Check the full
	// key BEFORE stripping a provider/model prefix for hardcoded fallbacks.
	fetchedMu.RLock()
	if fetchedRates != nil {
		if r, ok := fetchedRates[fullKey]; ok {
			fetchedMu.RUnlock()
			return r, fullKey + " (fetched)", true
		}
		if providerKey != "" {
			pkey := providerKey + "/" + fullKey
			if r, ok := fetchedRates[pkey]; ok {
				fetchedMu.RUnlock()
				return r, pkey + " (fetched)", true
			}
		}
	}
	fetchedMu.RUnlock()
	// Strip provider prefix: "openai/gpt-4o" -> "gpt-4o"
	if i := strings.LastIndexByte(modelID, '/'); i >= 0 && i < len(modelID)-1 {
		modelID = modelID[i+1:]
	}
	modelID = stripDateSuffix(modelID)
	key := strings.ToLower(strings.TrimSpace(modelID))
	// F28: check fetched rates for stripped canonical IDs too.
	fetchedMu.RLock()
	if fetchedRates != nil {
		if r, ok := fetchedRates[key]; ok {
			fetchedMu.RUnlock()
			return r, key + " (fetched)", true
		}
	}
	fetchedMu.RUnlock()
	if r, ok := modelRates[key]; ok {
		return r, key, true
	}
	return Rate{}, "", false
}

// CostFor estimates the USD cost of a (in, out) token
// pair at the model's rate. Input/output of 0 returns 0.
// The result is rounded to 6 decimal places for stable
// display; underlying float is preserved if you need
// more precision.
func CostFor(modelID string, inputTokens, outputTokens int64) float64 {
	return CostForProvider("", modelID, inputTokens, outputTokens)
}

// CostForProvider is CostFor with a known provider/endpoint, so a
// per-proxy price override (SetProviderRates) is applied. provider
// may be empty, in which case it behaves exactly like CostFor.
func CostForProvider(provider, modelID string, inputTokens, outputTokens int64) float64 {
	rate, _ := RateForProvider(provider, modelID)
	cost, _ := CostAtRate(rate, inputTokens, outputTokens, 0)
	return cost
}

// CostAtRate estimates cost with cached input priced separately when the
// source supplied that rate. cachedInputTokens is a subset of inputTokens.
// The boolean reports whether a distinct cache rate was available (or no
// cached tokens were present); false means cached input was conservatively
// charged at the normal input rate.
func CostAtRate(rate Rate, inputTokens, outputTokens, cachedInputTokens int64) (float64, bool) {
	if inputTokens < 0 {
		inputTokens = 0
	}
	if outputTokens < 0 {
		outputTokens = 0
	}
	if cachedInputTokens < 0 {
		cachedInputTokens = 0
	}
	if cachedInputTokens > inputTokens {
		cachedInputTokens = inputTokens
	}
	if inputTokens == 0 && outputTokens == 0 {
		return 0, true
	}
	evaluated := inputTokens - cachedInputTokens
	cacheRate := rate.CachedInputPer1k
	cacheRateKnown := cachedInputTokens == 0 || cacheRate > 0
	if cacheRate <= 0 {
		cacheRate = rate.InputPer1k
	}
	in := float64(evaluated)/1000.0*rate.InputPer1k + float64(cachedInputTokens)/1000.0*cacheRate
	out := float64(outputTokens) / 1000.0 * rate.OutputPer1k
	return roundUSD(in + out), cacheRateKnown
}

func roundUSD(v float64) float64 {
	// 1e-6 dollar precision is enough for small token batches
	// but it keeps floats from showing as 0.0000012
	// in the status bar.
	return float64(int64(v*1_000_000+0.5)) / 1_000_000
}

// FormatUSD renders a USD amount as "$0.00", "$0.01",
// or "$1234" depending on magnitude. Negative amounts
// get a leading "-". Non-zero sub-cent amounts retain enough precision not
// to be confused with a genuinely free request.
func FormatUSD(v float64) string {
	if v < 0 {
		return "-" + FormatUSD(-v)
	}
	switch {
	case v == 0:
		return "$0.00"
	case v < 0.01:
		s := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", v), "0"), ".")
		return "$" + s
	case v < 1000:
		return fmt.Sprintf("$%.2f", v)
	default:
		return fmt.Sprintf("$%.0f", v)
	}
}
