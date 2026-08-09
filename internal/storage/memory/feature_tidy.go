package memory

import "strings"

// feature_tidy.go: on-demand consolidation of near-identical
// preference/fact entries. The autosave deduplicates new writes
// (feature_autosave.go), but entries written by older builds or
// hand-saved duplicates can linger in the store; DedupSimilar
// sweeps those up so the briefing stays short and clean.

// DedupSimilar removes near-identical preference and fact entries,
// keeping the most recently updated one of each similar group.
// Returns the number of entries removed.
func (s *Store) DedupSimilar(scopes ...string) (int, error) {
	if len(scopes) == 0 {
		scopes = []string{ScopePreference, ScopeFact}
	}
	removed := 0
	for _, scope := range scopes {
		n, err := s.dedupScope(scope)
		if err != nil {
			return removed, err
		}
		removed += n
	}
	return removed, nil
}

func (s *Store) dedupScope(scope string) (int, error) {
	entries, err := s.List(scope, 1000)
	if err != nil {
		return 0, err
	}
	removed := 0
	kept := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if junkFact(e.Content) {
			// Garbage from old builds ("not explicitly stated",
			// gratitude filler) — delete outright.
			if err := s.Delete(e.ID); err == nil {
				removed++
			}
			continue
		}
		duplicate := false
		for _, k := range kept {
			if similarFact(e.Content, k.Content) {
				duplicate = true
				break
			}
		}
		if duplicate {
			if err := s.Delete(e.ID); err == nil {
				removed++
			}
			continue
		}
		kept = append(kept, e)
	}
	return removed, nil
}

// similarFact is the public near-identity test used both by the
// autosave (containsSimilar) and the tidy sweep. Tokenization
// works on the raw (lower-cased) text — normalizeFact strips
// spaces and would merge every token into one.
//
// Rule: symmetric difference (tokens in only one of the two) must
// be <= 2 — anything larger is a genuinely different fact. When
// the difference is exactly 2, the shared core must also cover
// >= 80% of the smaller token set, so "works on SuperCli" vs
// "works on NestCafe" (same verbs, different subject) stays
// distinct while "name is Maks" vs "Użytkownik ma na imię Maks."
// (translation) collapses.
func similarFact(a, b string) bool {
	ta := factTokens(strings.ToLower(a))
	tb := factTokens(strings.ToLower(b))
	if len(ta) == 0 || len(tb) == 0 {
		return false
	}
	shared := 0
	for _, t := range ta {
		for _, u := range tb {
			if t == u {
				shared++
				break
			}
		}
	}
	diff := (len(ta) - shared) + (len(tb) - shared)
	if diff > 2 {
		return false
	}
	if diff <= 1 {
		return true // one side is a subset of the other
	}
	minLen := len(ta)
	if len(tb) < minLen {
		minLen = len(tb)
	}
	if minLen == 0 {
		return false
	}
	return float64(shared)/float64(minLen) >= 0.8
}
