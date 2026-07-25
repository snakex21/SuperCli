// Model-availability errors: the provider rejecting the configured model.
//
// SuperCli already knows which models a provider serves — every provider scan
// stores the /v1/models inventory in config.toml (ProviderConf.CachedModels).
// Before this file the user saw nothing but the raw body, in whatever language
// the gateway happens to speak:
//
//	http 404: {"error":"当前 API 不支持所选模型 gpt-5.6-sol","type":"error"}
//
// That is a dead end. The repair — the list of models this endpoint does serve
// — costs nothing to carry and belongs in the failure itself, the same way an
// unknown tool argument answers with the argument names the schema declares.
// This is a configuration error shown to a human, not text in a prompt, so the
// usual token thrift does not apply.
//
// There is deliberately no automatic rescan on this error. The case that
// prompted the work disproves the premise: anyrouter's /v1/models still lists
// gpt-5.6-sol while /chat/completions answers 404 for it, so a refresh would
// spend a request to fetch the same list back. The inventory is refreshed when
// the user scans, and the message says so.
package llm

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// providerModelCatalog remembers the model inventory per endpoint. The key is
// the base URL rather than the provider's config.toml name because that is the
// only identity a transport has: llm.OpenAIConfig/AnthropicConfig carry a
// BaseURL, never the ProviderConf they were built from.
var providerModelCatalog = struct {
	sync.RWMutex
	byBaseURL map[string][]string
}{byBaseURL: make(map[string][]string)}

// providerCatalogKey normalizes a base URL so the value stored at provider
// scan time is found again from a transport, where the URL has already been
// through ResolveOpenAIEndpoints (case, trailing slash).
func providerCatalogKey(baseURL string) string {
	return strings.TrimRight(strings.ToLower(strings.TrimSpace(baseURL)), "/")
}

// RememberProviderModels records the model ids an endpoint advertises. It is
// called by the provider manager both when config.toml is loaded (cached
// inventory) and after a live scan. An empty list clears the entry: no
// knowledge is better than stale knowledge presented as fact.
func RememberProviderModels(baseURL string, models []string) {
	key := providerCatalogKey(baseURL)
	if key == "" {
		return
	}
	ids := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if _, dup := seen[m]; dup {
			continue
		}
		seen[m] = struct{}{}
		ids = append(ids, m)
	}
	providerModelCatalog.Lock()
	defer providerModelCatalog.Unlock()
	if len(ids) == 0 {
		delete(providerModelCatalog.byBaseURL, key)
		return
	}
	sort.Strings(ids)
	providerModelCatalog.byBaseURL[key] = ids
}

// ProviderModels returns the remembered inventory for an endpoint, or nil.
func ProviderModels(baseURL string) []string {
	key := providerCatalogKey(baseURL)
	if key == "" {
		return nil
	}
	providerModelCatalog.RLock()
	defer providerModelCatalog.RUnlock()
	ids := providerModelCatalog.byBaseURL[key]
	if len(ids) == 0 {
		return nil
	}
	return append([]string(nil), ids...)
}

func clearProviderModelCatalog() {
	providerModelCatalog.Lock()
	providerModelCatalog.byBaseURL = make(map[string][]string)
	providerModelCatalog.Unlock()
}

// modelUnavailableListLimit caps the models named in one error. Routers serve
// hundreds; the point is to make the next move obvious, not to dump a catalog.
const modelUnavailableListLimit = 10

// modelRejectionCodes are the machine-readable markers gateways use for this
// class. They are matched only inside an error payload, and only alongside a
// 4xx, so they cannot fire on ordinary prose.
var modelRejectionCodes = []string{
	"model_not_found",
	"model_not_supported",
	"unsupported_model",
	"invalid_model",
	"unknown_model",
	"model_unavailable",
	"model_deprecated",
	"does_not_exist",
}

// requestParamWords name a request field other than the model. When the body
// blames one of these, the provider is rejecting a parameter (reasoning effort,
// token budget, an image part) and not the model itself — even though such
// messages often quote the model id too. Staying quiet here keeps the hint
// from confidently pointing at the wrong thing.
var requestParamWords = []string{
	"reasoning", "effort", "thinking", "budget_tokens",
	"max_tokens", "max_completion_tokens", "temperature", "top_p",
	"tool_choice", "response_format", "image", "content block",
	"context_length_exceeded", "too long", "too large",
}

// isModelUnavailableStatus reports whether a status code can carry this class.
// Providers are wildly inconsistent — anyrouter answers 404 for a model it does
// not route and 400 for one it retired, OpenAI uses 404, others 400 or 422 — so
// the code alone decides nothing. Its job here is only to rule out the statuses
// that always mean something else: auth (401/403), quota (429), payload size
// (413), and every 5xx.
func isModelUnavailableStatus(status int) bool {
	switch status {
	case 401, 402, 403, 408, 413, 429:
		return false
	}
	return status >= 400 && status < 500
}

// errorTextFromBody extracts the human-readable part of an error payload.
// It handles {"error":"..."} (anyrouter), {"error":{"message":...}} (OpenAI,
// Anthropic) and {"message":...}, falling back to the raw body — which also
// covers a plain-text or HTML response.
func errorTextFromBody(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return ""
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return trimmed
	}
	parts := make([]string, 0, 3)
	add := func(rm json.RawMessage) {
		var s string
		if err := json.Unmarshal(rm, &s); err == nil {
			parts = append(parts, s)
			return
		}
		var obj struct {
			Message string          `json:"message"`
			Code    json.RawMessage `json:"code"`
			Type    string          `json:"type"`
			Param   string          `json:"param"`
		}
		if err := json.Unmarshal(rm, &obj); err != nil {
			return
		}
		parts = append(parts, obj.Message, obj.Type, obj.Param)
		var code string
		if json.Unmarshal(obj.Code, &code) == nil {
			parts = append(parts, code)
		}
	}
	if e, ok := raw["error"]; ok {
		add(e)
	}
	if m, ok := raw["message"]; ok {
		add(m)
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return trimmed
	}
	return strings.Join(out, " ")
}

// looksLikeModelUnavailable decides whether a non-2xx response is the provider
// saying it does not serve this model.
//
// The two signals are deliberately language-independent, because the message
// that prompted this code is Chinese and the next one will be something else:
//
//  1. the configured model id appears verbatim in the error text — a gateway
//     echoes the model only when the model is what it is complaining about; or
//  2. the payload carries one of the machine-readable rejection codes.
//
// Either signal must be accompanied by a status that can mean this (see
// isModelUnavailableStatus) and by the absence of a request-parameter
// complaint, which is the one realistic way an echoed model id misleads.
func looksLikeModelUnavailable(model string, status int, body []byte) bool {
	model = strings.TrimSpace(model)
	// Very short ids would match by accident inside a URL or a word, and an id
	// that short is not what any real gateway serves.
	if len(model) < 3 || !isModelUnavailableStatus(status) {
		return false
	}
	text := errorTextFromBody(body)
	if text == "" {
		return false
	}
	low := strings.ToLower(text)
	for _, w := range requestParamWords {
		if strings.Contains(low, w) {
			return false
		}
	}
	if strings.Contains(strings.ToLower(text), strings.ToLower(model)) {
		return true
	}
	for _, code := range modelRejectionCodes {
		if strings.Contains(low, code) {
			return true
		}
	}
	return false
}

// modelUnavailableHint renders the repair: the models this endpoint is known to
// serve, plus the nearest name when the configured one is an obvious variant of
// a real one. It returns "" when the response is not this class of error.
func modelUnavailableHint(baseURL, model string, status int, body []byte) string {
	if !looksLikeModelUnavailable(model, status, body) {
		return ""
	}
	available := ProviderModels(baseURL)
	var b strings.Builder
	b.WriteString(fmt.Sprintf(" — model %q is not available at this provider", model))
	if near := nearestModel(model, available); near != "" {
		b.WriteString(fmt.Sprintf(" (did you mean %q?)", near))
	}
	shown := make([]string, 0, len(available))
	for _, id := range available {
		if !strings.EqualFold(id, model) {
			shown = append(shown, id)
		}
	}
	// Same-family names first, alphabetical within each group. When the list is
	// truncated this is what decides whether a user who asked for gpt-5.6-sol
	// sees the endpoint's other gpt-* models or ten unrelated ones.
	family := modelFamilyPrefix(model)
	sort.SliceStable(shown, func(i, j int) bool {
		a := modelFamilyPrefix(shown[i]) == family
		b := modelFamilyPrefix(shown[j]) == family
		return a && !b
	})
	if len(shown) == 0 {
		b.WriteString("; no model list is cached for this endpoint — scan the provider to fetch one")
		return b.String()
	}
	more := 0
	if len(shown) > modelUnavailableListLimit {
		more = len(shown) - modelUnavailableListLimit
		shown = shown[:modelUnavailableListLimit]
	}
	b.WriteString("; available: ")
	b.WriteString(strings.Join(shown, ", "))
	if more > 0 {
		b.WriteString(fmt.Sprintf(" and %d more", more))
	}
	b.WriteString(" (rescan the provider if this list is stale)")
	return b.String()
}

// nearestModel returns the available id the configured one most plausibly
// meant, or "" when there is no unambiguous answer. Silence on a tie is the
// point: a wrong suggestion is worse than none, and the full list is printed
// either way.
//
// Two rules, in order:
//
//   - Segment prefix. Retired or mistyped ids are usually a real id with a
//     suffix bolted on ("gpt-5.6-sol" for "gpt-5.6"), which Levenshtein cannot
//     see at any sane distance. The longest candidate that ends on a separator
//     boundary wins; two candidates of equal length mean silence.
//   - Typographic neighbour. One edit for short names, two from five
//     characters up — the same budget and the same tie rule as nearestArgument
//     in internal/tools/core, kept in step deliberately.
func nearestModel(model string, available []string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" || len(available) == 0 {
		return ""
	}
	bestPrefix, bestPrefixLen, prefixTies := "", 0, 0
	for _, candidate := range available {
		c := strings.ToLower(strings.TrimSpace(candidate))
		if c == "" || c == model || !isSegmentPrefix(c, model) {
			continue
		}
		switch {
		case len(c) > bestPrefixLen:
			bestPrefix, bestPrefixLen, prefixTies = candidate, len(c), 1
		case len(c) == bestPrefixLen:
			prefixTies++
		}
	}
	if bestPrefix != "" && prefixTies == 1 {
		return bestPrefix
	}
	limit := 1
	if len(model) >= 5 {
		limit = 2
	}
	best, bestDistance, ties := "", limit+1, 0
	for _, candidate := range available {
		c := strings.ToLower(strings.TrimSpace(candidate))
		if c == "" || c == model {
			continue
		}
		distance := modelEditDistance(model, c, limit)
		switch {
		case distance < bestDistance:
			best, bestDistance, ties = candidate, distance, 1
		case distance == bestDistance:
			ties++
		}
	}
	if bestDistance > limit || ties > 1 {
		return ""
	}
	return best
}

// modelFamilyPrefix is the leading segment of an id ("gpt" for "gpt-5.6-sol",
// "claude" for "claude-opus-4-7"), after dropping any namespace. It is only
// used for ordering, never for deciding what is available.
func modelFamilyPrefix(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if i := strings.LastIndexByte(id, '/'); i >= 0 {
		id = id[i+1:]
	}
	if i := strings.IndexAny(id, "-_.:@"); i > 0 {
		id = id[:i]
	}
	return id
}

// isSegmentPrefix reports whether prefix is a leading part of s that ends where
// an id segment ends, so "gpt-5.6" matches "gpt-5.6-sol" but "gpt-5" does not
// match "gpt-56".
func isSegmentPrefix(prefix, s string) bool {
	if prefix == "" || len(prefix) >= len(s) || !strings.HasPrefix(s, prefix) {
		return false
	}
	switch s[len(prefix)] {
	case '-', '_', ':', '/', '@', '.':
		return true
	}
	return false
}

// modelEditDistance is Levenshtein distance that gives up past limit. Model ids
// are short, so the two-row form is ample. It is a local copy rather than a
// shared helper because internal/tools may not be imported from internal/llm.
func modelEditDistance(a, b string, limit int) int {
	over := limit + 1
	if a == b {
		return 0
	}
	if len(a)-len(b) > limit || len(b)-len(a) > limit {
		return over
	}
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current[0] = i
		rowBest := current[0]
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min(current[j-1]+1, previous[j]+1, previous[j-1]+cost)
			rowBest = min(rowBest, current[j])
		}
		if rowBest > limit {
			return over
		}
		previous, current = current, previous
	}
	if previous[len(b)] > limit {
		return over
	}
	return previous[len(b)]
}

// providerErrorHint is the single repair suffix appended to a terminal non-2xx
// provider error. The three classes are near-disjoint; effort is checked first
// because a rejected reasoning parameter is the one message that can also quote
// the model id.
func providerErrorHint(baseURL, model string, status int, body []byte) string {
	if h := badRequestEffortHint(status, body); h != "" {
		return h
	}
	if h := modelUnavailableHint(baseURL, model, status, body); h != "" {
		return h
	}
	return rateLimitExhaustedHint(model, status)
}
