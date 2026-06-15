// Reasoning-effort support for OpenAI-family models.
//
// Providers differ in the exact accepted values. SuperCli accepts the superset
// in /reasoning, but learns the per-model supported subset from live backend
// validation errors (for example: gpt-5.5 supports low/medium/high/xhigh, but
// not minimal). That avoids brittle, hardcoded per-model tables.
package llm

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
)

// reasoningEffortLevel holds the configured level ("" = provider default).
var reasoningEffortLevel atomic.Value

// ReasoningEffortLevels are the accepted /reasoning arguments.
var ReasoningEffortLevels = []string{"none", "minimal", "low", "medium", "high", "xhigh"}

var reasoningEffortSupport = struct {
	sync.RWMutex
	byModel map[string][]string
}{byModel: make(map[string][]string)}

// SetReasoningEffort sets the process-global configured effort level.
// Empty string clears it (provider default). Returns an error for unknown levels.
func SetReasoningEffort(level string) error {
	level = strings.ToLower(strings.TrimSpace(level))
	if level != "" && !isValidReasoningEffort(level) {
		return fmt.Errorf("unknown reasoning effort %q (want %s)",
			level, strings.Join(ReasoningEffortLevels, "|"))
	}
	reasoningEffortLevel.Store(level)
	return nil
}

// ReasoningEffort returns the configured level ("" = provider default).
func ReasoningEffort() string {
	if v, ok := reasoningEffortLevel.Load().(string); ok {
		return v
	}
	return ""
}

func isValidReasoningEffort(level string) bool {
	for _, l := range ReasoningEffortLevels {
		if l == level {
			return true
		}
	}
	return false
}

func normalizeReasoningModel(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndexByte(m, '/'); i >= 0 {
		m = m[i+1:]
	}
	return m
}

// SetReasoningEffortSupport records the levels the backend actually accepts
// for model. This is learned from API responses, not maintained as a static
// table.
func SetReasoningEffortSupport(model string, levels []string) {
	model = normalizeReasoningModel(model)
	if model == "" {
		return
	}
	seen := make(map[string]bool)
	out := make([]string, 0, len(levels))
	for _, l := range levels {
		l = strings.ToLower(strings.TrimSpace(l))
		if !isValidReasoningEffort(l) || seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	if len(out) == 0 {
		return
	}
	reasoningEffortSupport.Lock()
	reasoningEffortSupport.byModel[model] = out
	reasoningEffortSupport.Unlock()
}

// SupportedReasoningEfforts returns levels learned from the backend for model.
func SupportedReasoningEfforts(model string) ([]string, bool) {
	model = normalizeReasoningModel(model)
	reasoningEffortSupport.RLock()
	levels, ok := reasoningEffortSupport.byModel[model]
	reasoningEffortSupport.RUnlock()
	if !ok {
		return nil, false
	}
	return append([]string(nil), levels...), true
}

func clearReasoningEffortSupport() {
	reasoningEffortSupport.Lock()
	reasoningEffortSupport.byModel = make(map[string][]string)
	reasoningEffortSupport.Unlock()
}

func effortRank(level string) int {
	switch level {
	case "none":
		return 0
	case "minimal":
		return 1
	case "low":
		return 2
	case "medium":
		return 3
	case "high":
		return 4
	case "xhigh":
		return 5
	default:
		return -1
	}
}

func hasEffort(levels []string, level string) bool {
	for _, l := range levels {
		if l == level {
			return true
		}
	}
	return false
}

func nearestSupportedEffort(want string, supported []string) string {
	if want == "" || len(supported) == 0 || hasEffort(supported, want) {
		return want
	}
	wantRank := effortRank(want)
	if wantRank < 0 {
		return want
	}
	bestLower, bestLowerRank := "", -1
	bestHigher, bestHigherRank := "", 99
	hasNone := false
	for _, l := range supported {
		r := effortRank(l)
		if r < 0 {
			continue
		}
		if l == "none" {
			hasNone = true
			continue
		}
		if r < wantRank && r > bestLowerRank {
			bestLower, bestLowerRank = l, r
		}
		if r > wantRank && r < bestHigherRank {
			bestHigher, bestHigherRank = l, r
		}
	}
	if bestLower != "" {
		return bestLower
	}
	if bestHigher != "" {
		return bestHigher
	}
	if hasNone {
		return "none"
	}
	return want
}

// ReasoningEffortForModel returns the configured effort adjusted by live
// backend evidence for model.
func ReasoningEffortForModel(model string) string {
	eff := ReasoningEffort()
	if eff == "" || !SupportsReasoningEffort(model) {
		return ""
	}
	if supported, ok := SupportedReasoningEfforts(model); ok {
		return nearestSupportedEffort(eff, supported)
	}
	return eff
}

func ReasoningEffortAdjustment(model string) (configured, effective string, adjusted bool) {
	configured = ReasoningEffort()
	effective = ReasoningEffortForModel(model)
	return configured, effective, configured != "" && effective != "" && configured != effective
}

// SupportsXHighReasoningEffort reports whether the model accepts xhigh. When
// live backend evidence exists, it wins over the fallback family heuristic.
func SupportsXHighReasoningEffort(model string) bool {
	if supported, ok := SupportedReasoningEfforts(model); ok {
		return hasEffort(supported, "xhigh")
	}
	m := normalizeReasoningModel(model)
	return strings.Contains(m, "codex") || strings.HasPrefix(m, "gpt-5.5")
}

// ReasoningEffortErrorHint returns a readable suffix for HTTP 400 effort
// validation failures.
func ReasoningEffortErrorHint(body string) string {
	eff := ReasoningEffort()
	if eff == "" {
		return ""
	}
	if info, ok := ParseReasoningEffortError(body); ok && len(info.Supported) > 0 {
		fallback := nearestSupportedEffort(eff, info.Supported)
		return fmt.Sprintf(" — backend nie przyjal reasoning effort %q; obslugiwane: %s; SuperCli uzyje %q przy ponowieniu (albo /reasoning off)",
			eff, strings.Join(info.Supported, ", "), fallback)
	}
	b := strings.ToLower(body)
	if !strings.Contains(b, "reasoning") && !strings.Contains(b, "effort") {
		return ""
	}
	return fmt.Sprintf(" — backend nie przyjal reasoning effort %q; sprawdz /reasoning albo ustaw /reasoning off", eff)
}

func badRequestEffortHint(status int, body []byte) string {
	if status != 400 {
		return ""
	}
	return ReasoningEffortErrorHint(string(body))
}

type ReasoningEffortError struct {
	Requested string
	Supported []string
	Message   string
}

var quotedEffortRe = regexp.MustCompile(`'([^']+)'`)

// ParseReasoningEffortError extracts supported values from backend errors like:
// "Unsupported value: 'minimal' ... Supported values are: 'none', 'low', ...".
func ParseReasoningEffortError(body string) (ReasoningEffortError, bool) {
	body = strings.TrimSpace(body)
	if body == "" {
		return ReasoningEffortError{}, false
	}
	msg := body
	param := ""
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Param   string `json:"param"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err == nil {
		if payload.Error.Message != "" {
			msg = payload.Error.Message
		}
		param = payload.Error.Param
	}
	low := strings.ToLower(msg + " " + param)
	if !strings.Contains(low, "reasoning") && !strings.Contains(low, "effort") {
		return ReasoningEffortError{}, false
	}
	info := ReasoningEffortError{Message: msg}
	if idx := strings.Index(strings.ToLower(msg), "unsupported value:"); idx >= 0 {
		if m := quotedEffortRe.FindStringSubmatch(msg[idx:]); len(m) == 2 {
			info.Requested = strings.ToLower(strings.TrimSpace(m[1]))
		}
	}
	supportedPart := msg
	if idx := strings.Index(strings.ToLower(msg), "supported values"); idx >= 0 {
		supportedPart = msg[idx:]
	}
	for _, m := range quotedEffortRe.FindAllStringSubmatch(supportedPart, -1) {
		if len(m) != 2 {
			continue
		}
		v := strings.ToLower(strings.TrimSpace(m[1]))
		if isValidReasoningEffort(v) && !hasEffort(info.Supported, v) {
			info.Supported = append(info.Supported, v)
		}
	}
	return info, info.Requested != "" || len(info.Supported) > 0
}

// LearnReasoningEffortFromError records effort support discovered from a 400
// response and returns the effective retry value if the current config should
// be retried with a different effort.
func LearnReasoningEffortFromError(model string, status int, body []byte) (string, bool) {
	if status != 400 || ReasoningEffort() == "" {
		return "", false
	}
	info, ok := ParseReasoningEffortError(string(body))
	if !ok || len(info.Supported) == 0 {
		return "", false
	}
	SetReasoningEffortSupport(model, info.Supported)
	effective := ReasoningEffortForModel(model)
	if effective == "" || effective == ReasoningEffort() {
		return "", false
	}
	return effective, true
}

// NextReasoningEffort returns the level that follows current in the UI cycle.
func NextReasoningEffort(current string, xhigh bool) string {
	order := []string{"", "minimal", "low", "medium", "high"}
	if xhigh {
		order = append(order, "xhigh")
	}
	for i, l := range order {
		if l == current {
			return order[(i+1)%len(order)]
		}
	}
	return ""
}

// SupportsReasoningEffort reports whether SuperCli should try the reasoning
// effort parameter. Runtime evidence wins; otherwise we keep a conservative
// family fallback for first contact with OpenAI/Codex reasoning models.
func SupportsReasoningEffort(model string) bool {
	if supported, ok := SupportedReasoningEfforts(model); ok {
		return len(supported) > 0
	}
	m := normalizeReasoningModel(model)
	switch {
	case strings.HasPrefix(m, "gpt-5"):
		return true
	case strings.HasPrefix(m, "codex-") || m == "codex":
		return true
	case strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4"):
		return true
	}
	return false
}
