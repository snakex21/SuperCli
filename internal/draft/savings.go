// savings.go computes the per-step savings the F11
// marker shows. The savings number is heuristic — we
// don't have a counterfactual (verifier output WITHOUT
// the draft), so we approximate:
//
//   savings = max(0, draft_output_tokens - verifier_output_tokens)
//
// Rationale: if the draft produced 200 tokens and the
// verifier produced 80 tokens, the draft clearly helped
// the verifier skip a long plan (200 - 80 = 120 saved).
// If the verifier produced more than the draft, the
// draft was either ignored or the verifier added a lot
// of detail; either way savings is 0 (we don't claim
// negative savings in the UI).
//
// Override detection: a verifier is treated as
// "overriding" the draft when the Jaccard similarity
// of the (normalized) draft and verifier text is below
// the override threshold (0.30 by default). The override
// signal is what the F5 reflector learns from; the
// savings number is what the TUI shows. They are
// independent dimensions: a step can have savings > 0
// AND be an override (e.g. draft gave a short plan the
// verifier used verbatim for 5 of 7 steps, then added
// 2 new ones — the verifier text will have ~0.5 jaccard
// with the draft, savings is 100 tokens, but it
// doesn't really count as "overridden" in spirit. So
// we report both, separately).

package draft

import (
	"strings"
	"unicode"
)

// OverrideThreshold is the Jaccard similarity below
// which a verifier response is classified as an
// "override" of the draft. Empirically chosen: 0.3
// gives a reasonable signal that the two plans are
// talking about different things; below that the
// verifier essentially wrote its own plan.
const OverrideThreshold = 0.30

// Savings is the per-session counter the loop feeds
// events into. It is NOT thread-safe; the loop
// calls Add synchronously from a single goroutine.
type Savings struct {
	draftTokens   int
	verifyTokens  int
	savedTokens   int
	overrideCount int
	usedCount     int
}

// NewSavings returns a fresh counter.
func NewSavings() *Savings { return &Savings{} }

// Add records one (draft, verifier) pair and updates
// the counters. Returns (savings, decision).
//
// Decision rules:
//   - "injected" + savings=0 when draftText is empty
//     (draft failed silently, nothing to compare)
//   - "used" + savings>0 when the verifier's text
//     has Jaccard similarity >= OverrideThreshold
//     with the draft (i.e. the verifier echoed or
//     built on the draft's plan)
//   - "overridden" + savings=0 when the verifier
//     wrote a substantially different response
//
// Savings number when "used": the draft's output
// tokens. The intuition is "the draft paid for the
// verifier's plan, so those tokens are saved". The
// verifier's own token count doesn't enter the
// formula — the user-visible "savings" is the value
// the draft provided, regardless of how much the
// verifier then added.
//
// When token counts are zero (provider didn't
// report usage), we fall back to word counts.
func (s *Savings) Add(draftText, verifierText string, draftTokens, verifyTokens int) (savings int, decision string) {
	if s == nil {
		return 0, "no-recorder"
	}
	dt := strings.TrimSpace(draftText)
	vt := strings.TrimSpace(verifierText)
	if dt == "" {
		return 0, "injected"
	}
	s.draftTokens += draftTokens
	s.verifyTokens += verifyTokens

	j := jaccard(tokenize(dt), tokenize(vt))
	if j >= OverrideThreshold {
		// Verifier relied on the draft. Savings
		// = draft's output tokens (what the
		// draft "paid" for the verifier).
		savings = draftTokens
		if savings == 0 {
			savings = wordCount(dt)
		}
		s.savedTokens += savings
		s.usedCount++
		return savings, "used"
	}
	s.overrideCount++
	return 0, "overridden"
}

// TotalSaved returns the cumulative savings across
// all Add calls.
func (s *Savings) TotalSaved() int {
	if s == nil {
		return 0
	}
	return s.savedTokens
}

// UsedCount is the number of Add calls classified as
// "used" (verifier echoed draft).
func (s *Savings) UsedCount() int {
	if s == nil {
		return 0
	}
	return s.usedCount
}

// OverrideCount is the number of Add calls classified
// as "overridden" (verifier wrote its own plan).
func (s *Savings) OverrideCount() int {
	if s == nil {
		return 0
	}
	return s.overrideCount
}

// tokenize splits a string into a lowercased set of
// "word" tokens. We strip punctuation and Unicode
// categories that are not letters/digits. Used for
// the Jaccard overlap computation.
func tokenize(s string) map[string]struct{} {
	out := make(map[string]struct{})
	cur := make([]rune, 0, 16)
	flush := func() {
		if len(cur) == 0 {
			return
		}
		out[strings.ToLower(string(cur))] = struct{}{}
		cur = cur[:0]
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur = append(cur, r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

// jaccard returns |A ∩ B| / |A ∪ B| for two token
// sets. Returns 1.0 for two empty sets (perfectly
// similar — they're both nothing). Returns 0.0
// when one is empty and the other isn't.
func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1.0
	}
	if len(a) == 0 || len(b) == 0 {
		return 0.0
	}
	intersect := 0
	for k := range a {
		if _, ok := b[k]; ok {
			intersect++
		}
	}
	union := len(a) + len(b) - intersect
	return float64(intersect) / float64(union)
}

// wordCount is the cheap fallback when the provider
// does not report usage. Words are whitespace-
// separated runes. Empty string → 0.
func wordCount(s string) int {
	return len(strings.Fields(s))
}
