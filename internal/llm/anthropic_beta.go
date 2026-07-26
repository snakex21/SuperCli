// Beta-feature gates: an Anthropic-dialect endpoint refusing a request until a
// specific `anthropic-beta` feature is opted into.
//
// The case that prompted this file is a gateway (anyrouter) that answers every
// request for its Claude models with
//
//	http 400: {"error":"1m 上下文已经全量可用，请启用 1m 上下文后重试","type":"error"}
//
// — "the 1M context is fully available, enable 1M context and retry". The
// repair is one header, `anthropic-beta: context-1m-2025-08-07`, and the error
// is the only place that says so. SuperCli never sent the header, so every
// model behind that gate was simply unreachable.
//
// Two ways to fix that. Sending the header proactively on every Anthropic
// request is simpler, but it spends a promise SuperCli cannot keep: a beta flag
// the account is not entitled to may be rejected by the real API, and that
// would break the working case to fix the broken one. There is no way to verify
// that from here without an entitled account, and an unverified assumption that
// costs the healthy path is the wrong trade. So the header is sent only where
// an endpoint has said it wants it — the cost is paid once, on the mistake,
// which is where a repair belongs.
//
// The recognition is deliberately narrow: one status code, one retry, remembered
// per endpoint so the second turn goes out correct. It is also independent of
// language — the message that prompted this is Chinese and the next one will be
// something else — so it keys on the feature's own vocabulary ("1m" next to a
// word for context) rather than on any phrasing, with the same exclusion list
// discipline as looksLikeModelUnavailable.
package llm

import (
	"regexp"
	"sort"
	"strings"
	"sync"
)

// anthropicBetaContext1M is the beta feature id for the 1M-token context
// window. It is the only gate observed in the wild so far.
const anthropicBetaContext1M = "context-1m-2025-08-07"

// betaGate describes one recognizable "you must opt into this beta" refusal.
type betaGate struct {
	// header is the anthropic-beta value that opens the gate.
	header string
	// matches reports whether the lowercased error text is this gate. It is
	// only ever called on a 400 whose text has already survived betaGateExcluded.
	matches func(low string) bool
}

// oneMillionTokenRe matches the "1m" / "1000k" size marker as a standalone
// token. Substring matching would fire inside model ids and hashes ("gpt-4-1m",
// "a1m3f"), so the boundaries are required.
var oneMillionTokenRe = regexp.MustCompile(`(^|[^0-9a-z])(1m|1000k)([^0-9a-z]|$)`)

// contextWords name the context window in the languages gateways actually
// answer in. English and Chinese cover every observed case; the list is here so
// the next one is a one-line change rather than a new heuristic.
var contextWords = []string{"context", "上下文", "コンテキスト", "컨텍스트"}

// betaGates is the recognized set. Ordered, first match wins.
var betaGates = []betaGate{{
	header: anthropicBetaContext1M,
	matches: func(low string) bool {
		if !oneMillionTokenRe.MatchString(low) {
			return false
		}
		for _, w := range contextWords {
			if strings.Contains(low, w) {
				return true
			}
		}
		return false
	},
}}

// betaGateExcluded rules out the refusals that talk about the context window
// but are not a beta gate. The dangerous neighbour is an overflow: "prompt is
// too long: 1.1m tokens > 1m maximum" names both a size and the context, and
// retrying it with a header would only hide a real problem. Model-rejection
// codes are excluded for the same reason — a 400 naming a model SuperCli cannot
// use is answered by modelUnavailableHint, not by a retry.
var betaGateExclusions = []string{
	"exceed", "too long", "too large", "too many",
	"context_length_exceeded", "context length exceeded",
	"超过", "过长", "太长",
	"max_tokens", "invalid_api_key", "insufficient",
}

func betaGateExcluded(low string) bool {
	for _, w := range betaGateExclusions {
		if strings.Contains(low, w) {
			return true
		}
	}
	for _, code := range modelRejectionCodes {
		if strings.Contains(low, code) {
			return true
		}
	}
	return false
}

// looksLikeBetaRequired decides whether a non-2xx response is the endpoint
// asking for an anthropic-beta opt-in, and returns the header value that
// answers it.
//
// Only 400 qualifies. This class is a request-validation refusal: the endpoint
// understood the request and declined its shape. 401/403 mean credentials,
// 404 means the route or model, 429/5xx mean capacity — none of them are
// repaired by a header, and retrying them here would mask them. The live
// evidence agrees: on the same gateway the gated models answer 400 while a
// model without the gate answers 429.
func looksLikeBetaRequired(status int, body []byte) (string, bool) {
	if status != 400 {
		return "", false
	}
	text := errorTextFromBody(body)
	if text == "" {
		return "", false
	}
	low := strings.ToLower(text)
	// An endpoint that names the feature id outright is unambiguous, and
	// saying it does not make the exclusions irrelevant — a "prompt too long"
	// that happens to quote the header is still an overflow.
	if betaGateExcluded(low) {
		return "", false
	}
	for _, g := range betaGates {
		if strings.Contains(low, g.header) || g.matches(low) {
			return g.header, true
		}
	}
	return "", false
}

// endpointBetaFeatures remembers the beta features an endpoint has demanded, so
// only the first request of a session pays the extra round trip. The key is the
// base URL for the same reason as providerModelCatalog: a transport carries an
// AnthropicConfig, never the ProviderConf it was built from.
var endpointBetaFeatures = struct {
	sync.RWMutex
	byBaseURL map[string]map[string]struct{}
}{byBaseURL: make(map[string]map[string]struct{})}

// rememberEndpointBeta records that baseURL requires header.
func rememberEndpointBeta(baseURL, header string) {
	key := providerCatalogKey(baseURL)
	if key == "" || header == "" {
		return
	}
	endpointBetaFeatures.Lock()
	defer endpointBetaFeatures.Unlock()
	if endpointBetaFeatures.byBaseURL[key] == nil {
		endpointBetaFeatures.byBaseURL[key] = make(map[string]struct{}, 1)
	}
	endpointBetaFeatures.byBaseURL[key][header] = struct{}{}
}

// endpointRequiresBeta reports whether header is already known for baseURL. It
// is what stops a retry loop: a gate that stays closed after the header was
// sent is a real error, not another chance.
func endpointRequiresBeta(baseURL, header string) bool {
	key := providerCatalogKey(baseURL)
	if key == "" {
		return false
	}
	endpointBetaFeatures.RLock()
	defer endpointBetaFeatures.RUnlock()
	_, ok := endpointBetaFeatures.byBaseURL[key][header]
	return ok
}

// endpointBetaHeader renders the anthropic-beta header value for baseURL, or ""
// when the endpoint has never asked for one. Sorted so the header is stable
// across turns — a value that reshuffles would be a prompt-cache hazard on
// endpoints that key on request headers.
func endpointBetaHeader(baseURL string) string {
	key := providerCatalogKey(baseURL)
	if key == "" {
		return ""
	}
	endpointBetaFeatures.RLock()
	features := make([]string, 0, len(endpointBetaFeatures.byBaseURL[key]))
	for f := range endpointBetaFeatures.byBaseURL[key] {
		features = append(features, f)
	}
	endpointBetaFeatures.RUnlock()
	if len(features) == 0 {
		return ""
	}
	sort.Strings(features)
	return strings.Join(features, ",")
}

func clearEndpointBetas() {
	endpointBetaFeatures.Lock()
	endpointBetaFeatures.byBaseURL = make(map[string]map[string]struct{})
	endpointBetaFeatures.Unlock()
}
