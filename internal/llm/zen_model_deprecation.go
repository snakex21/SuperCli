// OpenCode Zen retires free models in place: the configured id disappears
// from /zen/v1/models and chat answers HTTP 410 with an error type of
// ModelDeprecated that names the successor in metadata.replacement:
//
//	{"type":"error",
//	 "error":{"type":"ModelDeprecated",
//	          "message":"Model mimo-v2.5-free has been deprecated. Use mimo-v2.6-flash-free instead."},
//	 "metadata":{"model":"mimo-v2.5-free","replacement":"mimo-v2.6-flash-free"}}
//
// The repair is unambiguous, so the transport learns it instead of dying:
// the request is retargeted to the successor and retried once, and later
// builds of the same configured model go straight to the successor. This is
// the same self-healing shape as the reasoning-effort and image-input
// learning — patch once, remember for the rest of the process.
//
// Everything here is scoped to Zen base URLs. Another provider's 410 or its
// prose about deprecation is not ours to interpret: only the Zen gateway
// documents this exact contract, and a misread replacement id would silently
// change which model a non-Zen provider is billed for.
package llm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// zenModelReplacementCache remembers successor ids per endpoint+model. The
// key mirrors providerCatalogKey (base URL) because a transport never knows
// the config.toml provider name it was built from; the model part is
// lower-cased so "Mimo-2.5-Free" and "mimo-v2.5-free" share one entry.
var zenModelReplacementCache = struct {
	sync.RWMutex
	byKey map[string]string
}{byKey: make(map[string]string)}

// rememberZenModelReplacement records that model on this endpoint was
// retired in favour of replacement. Invalid ids are dropped rather than
// stored: a bad value here would poison every later request.
func rememberZenModelReplacement(baseURL, model, replacement string) {
	model = strings.ToLower(strings.TrimSpace(model))
	replacement = sanitizeZenModelID(replacement)
	key := providerCatalogKey(baseURL)
	if key == "" || model == "" || replacement == "" {
		return
	}
	zenModelReplacementCache.Lock()
	defer zenModelReplacementCache.Unlock()
	zenModelReplacementCache.byKey[key+"|"+model] = replacement
}

// rememberZenReplacement stores the successor under both the id that was on
// the wire and the id the config asks for, which flattens a deprecation
// chain: once A→B and B→C are known, a build of A goes straight to C
// instead of replaying A→B→C one hop per turn.
func rememberZenReplacement(baseURL, configured, wire, replacement string) {
	rememberZenModelReplacement(baseURL, wire, replacement)
	if !strings.EqualFold(strings.TrimSpace(configured), strings.TrimSpace(wire)) {
		rememberZenModelReplacement(baseURL, configured, replacement)
	}
}

// zenEffectiveModel returns the remembered successor for model on baseURL,
// or model unchanged. Callers use it at request-build time so a remapped
// model does not pay a doomed 410 round-trip on every turn.
func zenEffectiveModel(baseURL, model string) string {
	key := providerCatalogKey(baseURL) + "|" + strings.ToLower(strings.TrimSpace(model))
	if key == "" {
		return model
	}
	zenModelReplacementCache.RLock()
	repl, ok := zenModelReplacementCache.byKey[key]
	zenModelReplacementCache.RUnlock()
	if !ok || repl == "" {
		return model
	}
	return repl
}

// clearZenModelReplacementCache drops every learned successor. Tests only:
// production entries live for the lifetime of the process, the same as the
// provider model catalog.
func clearZenModelReplacementCache() {
	zenModelReplacementCache.Lock()
	zenModelReplacementCache.byKey = make(map[string]string)
	zenModelReplacementCache.Unlock()
}

// zenModelDeprecation extracts the successor id from a Zen deprecation
// rejection, or "" when the response is not one (or names no successor).
//
// Two shapes are accepted, both observed on the live gateway: the full JSON
// envelope above, where metadata.replacement is authoritative, and a
// bare/plain-text message carrying "Use <id> instead". HTTP 410 alone marks
// the class, but without a parseable successor there is nothing to repair,
// so the result stays empty and the caller falls back to the ordinary
// unavailable-model hint.
func zenModelDeprecation(status int, body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return ""
	}
	var payload struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
		Metadata struct {
			Replacement string `json:"replacement"`
		} `json:"metadata"`
	}
	message := text
	replacement := ""
	if err := json.Unmarshal([]byte(text), &payload); err == nil {
		message = payload.Error.Message
		replacement = strings.TrimSpace(payload.Metadata.Replacement)
	}
	if !isZenDeprecation(status, payload.Error.Type, text, message) {
		return ""
	}
	if replacement == "" {
		replacement = zenUseInsteadModel(message)
	}
	return sanitizeZenModelID(replacement)
}

// isZenDeprecation recognises the class. HTTP 410 (Gone) is the status the
// gateway sends for a retired model, and ModelDeprecated / "has been
// deprecated" are the markers inside the body; either signal alone is
// enough on a Zen error response, because the parser only runs on non-2xx
// payloads and still needs a successor to do anything with it.
func isZenDeprecation(status int, errType, raw, message string) bool {
	if status == http.StatusGone {
		return true
	}
	if strings.Contains(strings.ToLower(errType), "deprecat") {
		return true
	}
	// No code field: fall back to the prose the gateway sends, either the
	// parsed message or the raw body when it was not JSON at all.
	if strings.Contains(strings.ToLower(message), "has been deprecated") {
		return true
	}
	return strings.Contains(strings.ToLower(raw), "has been deprecated")
}

// zenUseInsteadModel pulls the id out of "Use <id> instead" prose — the
// fallback when metadata.replacement is absent.
func zenUseInsteadModel(message string) string {
	low := strings.ToLower(message)
	i := strings.Index(low, "use ")
	if i < 0 {
		return ""
	}
	rest := message[i+len("use "):]
	j := strings.Index(strings.ToLower(rest), " instead")
	if j <= 0 {
		return ""
	}
	return sanitizeZenModelID(rest[:j])
}

// sanitizeZenModelID accepts only a plausible model id: short, no
// whitespace, no JSON/prose punctuation. Garbage never reaches the cache or
// a request body; the caller falls back to the generic error hint.
func sanitizeZenModelID(id string) string {
	id = strings.Trim(strings.TrimSpace(id), "`'\".,;:()[]{}")
	if len(id) < 2 || len(id) > 128 {
		return ""
	}
	for _, r := range id {
		if r <= ' ' || r == '"' || r == '\'' || r == '`' ||
			r == '{' || r == '}' || r == ',' || r == '<' || r == '>' {
			return ""
		}
	}
	return id
}

// retargetRequestModel rewrites the top-level "model" field of a built
// request body and reports the id it replaced. ok is false when the body is
// not JSON, carries no model, or already asks for model — in which case
// there is nothing to patch and nothing to remember.
func retargetRequestModel(body []byte, model string) (patched []byte, old string, ok bool) {
	model = sanitizeZenModelID(model)
	if model == "" {
		return nil, "", false
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, "", false
	}
	old, _ = req["model"].(string)
	if old == "" || strings.EqualFold(old, model) {
		return nil, old, false
	}
	req["model"] = model
	out, err := json.Marshal(req)
	if err != nil {
		return nil, old, false
	}
	return out, old, true
}

// zenDeprecationHint renders the replacement-first repair suffix: the exact
// id to switch to, not the generic "not available" list (which is usually
// stale — the retired id is only removed from /models after the fact).
// Empty for non-Zen endpoints and for responses that are not this class.
func zenDeprecationHint(baseURL, model string, status int, body []byte) string {
	if !isOpenCodeZenBaseURL(baseURL) {
		return ""
	}
	repl := zenModelDeprecation(status, body)
	if repl == "" || strings.EqualFold(repl, strings.TrimSpace(model)) {
		return ""
	}
	return fmt.Sprintf(" — model %q is deprecated at this provider; use %q instead", model, repl)
}
