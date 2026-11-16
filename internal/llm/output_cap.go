package llm

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Default output cap. Before 1.0.30 neither Chat Completions nor Responses
// sent max_tokens / max_output_tokens unless SUPERCLI_LLM_MAX_TOKENS was set,
// so a degenerate stream ran to the model maximum (128k tokens, 739 s in the
// 1.0.28 incident). 32000 is what the official opencode CLI sends on Zen and
// is far above any normal answer or edit (the tool-call argument cap is
// 256 KiB, ~64k tokens of escaped JSON at worst, ~30k typically); reasoning
// families budget hidden reasoning in the same field, so they get 64000.
const (
	DefaultMaxOutputTokens          = 32000
	DefaultReasoningMaxOutputTokens = 64000
)

// learnedOutputCaps remembers, per endpoint and model, a lower cap the
// provider named when it rejected the default (> 0) or that the field must be
// omitted (-1), so only the first request pays the rejected round trip.
var learnedOutputCaps sync.Map

func outputCapKey(baseURL, model string) string {
	return strings.TrimRight(strings.ToLower(baseURL), "/") + "|" + strings.ToLower(model)
}

// defaultOutputCapApplies keeps the default off where it could only hurt:
// local servers (their real limit is the loaded context; vLLM rejects
// max_tokens + prompt > max_model_len) and the OpenCode Zen / opencode
// gateway paths, whose request shape is fixed elsewhere.
func defaultOutputCapApplies(baseURL, capabilityModel string) bool {
	return !isLocalBaseURL(baseURL) && !isOpenCodeZenBaseURL(baseURL) &&
		!strings.HasPrefix(strings.ToLower(capabilityModel), "opencode/")
}

// defaultOutputCap is the cap sent when none is configured; 0 = omit.
func defaultOutputCap(baseURL, model string, reasoning bool) int {
	if v, ok := learnedOutputCaps.Load(outputCapKey(baseURL, model)); ok {
		if n := v.(int); n > 0 {
			return n
		}
		return 0
	}
	if reasoning {
		return DefaultReasoningMaxOutputTokens
	}
	return DefaultMaxOutputTokens
}

var outputCapParamWords = []string{
	"max_tokens", "max_completion_tokens", "max_output_tokens", "maxoutputtokens",
	"max output tokens", "maximum output tokens", "in the completion", "completion tokens",
}

var outputLimitPattern = regexp.MustCompile(`(?:at most|less than or equal to|<=|up to|no more than|maximum(?: value)?(?: allowed)?(?: is| of)?|\[\s*1\s*,)\s*:?\s*(\d{3,7})`)

// learnOutputCapRejection recognizes a 400/422 that rejects the DEFAULT
// output cap (too large for the model, or an unknown parameter) and returns
// the cap to retry with: the limit the error names when it is lower, else 0
// (omit the field). The decision is remembered for later requests.
func learnOutputCapRejection(baseURL, model string, sent, status int, body []byte) (int, bool) {
	if sent <= 0 || (status != http.StatusBadRequest && status != http.StatusUnprocessableEntity) {
		return 0, false
	}
	text := strings.ToLower(string(body))
	mentions := false
	for _, word := range outputCapParamWords {
		if strings.Contains(text, word) {
			mentions = true
			break
		}
	}
	if !mentions {
		return 0, false
	}
	next := -1
	// vLLM: "...you requested N tokens (M in the messages, K in the
	// completion)": the limit depends on the prompt, so omit the field.
	if !strings.Contains(text, "in the completion") {
		for _, m := range outputLimitPattern.FindAllStringSubmatch(text, -1) {
			if n, err := strconv.Atoi(m[1]); err == nil && n > 0 && n < sent {
				next = n
				break
			}
		}
	}
	learnedOutputCaps.Store(outputCapKey(baseURL, model), next)
	if next < 0 {
		return 0, true
	}
	return next, true
}

// withMaxOutputTokens sets (n > 0) or removes (n <= 0) the Responses
// max_output_tokens field of a JSON request body.
func withMaxOutputTokens(body []byte, n int) ([]byte, bool) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return body, false
	}
	if n > 0 {
		root["max_output_tokens"] = json.RawMessage(strconv.Itoa(n))
	} else {
		delete(root, "max_output_tokens")
	}
	out, err := json.Marshal(root)
	if err != nil {
		return body, false
	}
	return out, true
}
