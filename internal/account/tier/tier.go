// Package tier classifies models into "small" or "big" so the
// rest of the program can adapt prompt size and tool exposure
// without ever hardcoding model names.
//
// Classification cascade (first hit wins):
//
//  1. User rules from config.toml [[model_tiers]] — a
//     case-insensitive glob on the model name. None ship by
//     default.
//  2. Known price: combined input+output cost > 0 and
//     <= $2 per million tokens → small, more expensive → big.
//  3. Generic name parsing: a parameter count like "9b" or
//     "1.5b". A MoE active-parameter suffix "-a3b" takes
//     precedence (the ACTIVE parameter count is what matters
//     for capability). Under 12B → small. If no parameter
//     count is found, marker words (mini, nano, lite, tiny,
//     flash, haiku) → small.
//  4. Unknown → small (safer to under-promise: a small prompt
//     and a trimmed tool set work on every model).
package tier

import (
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Tier is the size class of a model.
type Tier string

const (
	Small Tier = "small"
	Big   Tier = "big"
)

// Rule is a user-supplied glob override from config.toml
// [[model_tiers]]. Pattern is matched case-insensitively
// against the model name; first matching rule wins.
type Rule struct {
	Pattern string
	Tier    string
}

// priceThreshold is the combined ($/M input + $/M output)
// cost at or under which a priced model counts as small.
const priceThreshold = 2.0

// paramThreshold is the parameter count (in billions) under
// which a model counts as small.
const paramThreshold = 12.0

var (
	// moeRe matches a MoE active-parameter suffix like
	// "-a3b" in "qwen3.5-35b-a3b".
	moeRe = regexp.MustCompile(`(?i)-a(\d+(?:\.\d+)?)b\b`)
	// paramRe matches a parameter count like "9b", "35b",
	// "1.5b". \b keeps it from matching inside words.
	paramRe = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)b\b`)
	// tokenSplit breaks a model name into words for the
	// marker check ("gpt-5-mini" → gpt, 5, mini) so
	// "gemini" does not match marker "mini".
	tokenSplit = regexp.MustCompile(`[^a-z0-9.]+`)
)

// markers are name fragments that signal a small/cheap model
// when no parameter count is present.
var markers = map[string]bool{
	"mini": true, "nano": true, "lite": true,
	"tiny": true, "flash": true, "haiku": true,
}

// ParseParams extracts the (active) parameter count, in
// billions, from a model name. A MoE "-aNb" suffix wins over
// the total count. Returns ok=false when the name carries no
// parameter count.
func ParseParams(model string) (billions float64, ok bool) {
	name := strings.ToLower(model)
	if m := moeRe.FindStringSubmatch(name); m != nil {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			return v, true
		}
	}
	best := -1.0
	for _, m := range paramRe.FindAllStringSubmatch(name, -1) {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil && v > best {
			best = v
		}
	}
	if best < 0 {
		return 0, false
	}
	return best, true
}

// hasMarker reports whether the model name contains a
// small-model marker word as a whole token.
func hasMarker(model string) bool {
	for _, tok := range tokenSplit.Split(strings.ToLower(model), -1) {
		if markers[tok] {
			return true
		}
	}
	return false
}

// Classify runs the cascade. inputCost/outputCost are $/M
// tokens (0 = unknown). rules come from config.toml and are
// checked first; first match wins.
func Classify(model string, inputCost, outputCost float64, rules []Rule) Tier {
	lower := strings.ToLower(model)
	for _, r := range rules {
		if r.Pattern == "" {
			continue
		}
		if matched, err := path.Match(strings.ToLower(r.Pattern), lower); err == nil && matched {
			if strings.EqualFold(strings.TrimSpace(r.Tier), string(Big)) {
				return Big
			}
			return Small
		}
	}
	if combined := inputCost + outputCost; combined > 0 {
		if combined <= priceThreshold {
			return Small
		}
		return Big
	}
	if b, ok := ParseParams(model); ok {
		if b < paramThreshold {
			return Small
		}
		return Big
	}
	if hasMarker(model) {
		return Small
	}
	return Small // unknown → small
}
