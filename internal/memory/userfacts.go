package memory

import (
	"fmt"
	"strings"
)

// This file is the DETERMINISTIC safety net under the LLM fact
// extractor (autosave.go / summaryPrompt). The small summarizer
// model frequently returns a bare "NOTHING" with no "USER: ..."
// line, so simple personal declarations ("lubię komputery", "mam
// na imię Maks", "my name is Anna") were lost across restarts.
//
// ExtractUserFacts scans RAW user messages with plain, cheap,
// allocation-light string matching — no LLM call, no network — and
// yields the same English "USER: ..."-style fact strings that
// saveUserFacts already knows how to persist (global ScopePreference
// with dedup). It runs synchronously on every user turn, independent
// of whether the LLM extractor produced anything.

// maxFactValue caps the captured value length so a runaway sentence
// ("I like very long things and also ...") never becomes a fact.
const maxFactValue = 80

// factPattern is one trigger phrase plus how to render the captured
// value as an English durable fact. kind groups name- vs
// preference-style patterns for sentence rendering.
type factPattern struct {
	prefix string // lower-cased phrase the message text must start with
	render func(value string) string
}

// namePatterns capture identity ("name is X"). The prefixes are
// matched case-insensitively against the start of a cleaned message
// (see matchPatterns).
var namePatterns = []factPattern{
	// Polish
	{"nazywam sie ", renderName},
	{"mam na imie ", renderName},
	// English
	{"my name is ", renderName},
	{"call me ", renderName},
	{"i am called ", renderName},
}

// jestemPatterns / imPatterns are the risky identity forms: "jestem
// X" / "i'm X" are often transient states ("jestem zmęczony", "i'm
// tired"), so the captured value is checked against transientStates
// before it becomes a name fact.
var jestemPrefixes = []string{"jestem ", "nazywam się "}
var imPrefixes = []string{"i'm ", "i am "}

// likePatterns capture standing preferences. Each renders the value
// into an English sentence; negative forms ("nie lubię", "i hate")
// render as dislikes.
var likePatterns = []factPattern{
	// Polish — negatives first so "nie lubię" wins over "lubię".
	{"nie lubie ", renderDislike},
	{"nie znosze ", renderDislike},
	{"lubie ", renderLike},
	{"uwielbiam ", renderLike},
	{"wole ", renderPrefer},
	{"preferuje ", renderPrefer},
	// English — negatives first.
	{"i don't like ", renderDislike},
	{"i dont like ", renderDislike},
	{"i do not like ", renderDislike},
	{"i hate ", renderDislike},
	{"i like ", renderLike},
	{"i love ", renderLike},
	{"i enjoy ", renderLike},
	{"i prefer ", renderPrefer},
}

// transientStates are values that must NOT become an identity fact
// after "jestem"/"i'm" — they are passing states, not who the user
// is. Stored fold-normalized (lower, diacritics-folded) for matching.
var transientStates = map[string]struct{}{}

func init() {
	for _, s := range []string{
		// Polish transient states / locations / feelings.
		"zmeczony", "zmeczona", "glodny", "glodna", "chory", "chora",
		"zajety", "zajeta", "gotowy", "gotowa", "pewny", "pewna",
		"w domu", "w pracy", "w drodze", "tutaj", "tu", "online",
		"zalogowany", "zalogowana", "smutny", "smutna", "szczesliwy",
		"szczesliwa", "spozniony", "spozniona", "wkurzony", "wkurzona",
		"ciekawy", "ciekawa", "nowy", "nowa", "programista", // role, but too generic for identity
		// English transient states.
		"tired", "done", "hungry", "sick", "busy", "ready", "sure",
		"here", "home", "back", "online", "fine", "ok", "okay", "good",
		"happy", "sad", "late", "lost", "confused", "curious", "new",
		"sorry", "right", "wrong", "afraid", "glad",
	} {
		transientStates[foldKey(s)] = struct{}{}
	}
}

// ExtractUserFacts scans raw user-message texts and returns durable
// personal facts rendered as English sentences (the same shape the
// "USER: " lines feed into saveUserFacts). It is conservative: when
// in doubt it returns nothing. Pure string matching — safe to call
// synchronously on the exit path.
func ExtractUserFacts(userMessages []string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, msg := range userMessages {
		for _, sentence := range splitSentences(msg) {
			if fact, ok := factFromSentence(sentence); ok {
				key := normalizeFact(fact)
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}
				out = append(out, fact)
			}
		}
	}
	return out
}

// factFromSentence tries every pattern against one cleaned sentence
// and returns the first English fact it can build. ok is false when
// the sentence holds no durable personal declaration.
func factFromSentence(sentence string) (string, bool) {
	clean := strings.TrimSpace(sentence)
	if clean == "" {
		return "", false
	}
	// Skip questions and imperatives-as-questions outright.
	if strings.Contains(clean, "?") {
		return "", false
	}
	// Build two rune-aligned views of the sentence: orig keeps the
	// user's real spelling (for the stored value); fold is
	// lower-cased + diacritics-folded (for prefix matching). They
	// share indices because foldRune is a 1:1 rune mapping.
	orig := []rune(clean)
	fold := make([]rune, len(orig))
	for i, r := range orig {
		fold[i] = foldRune(toLowerRune(r))
	}
	folded := string(fold)

	// 1. Unambiguous name patterns.
	if v, ok := matchPrefix(namePatterns, folded, orig); ok {
		return v, true
	}
	// 2. Preference patterns.
	if v, ok := matchPrefix(likePatterns, folded, orig); ok {
		return v, true
	}
	// 3. Risky identity: "jestem X" / "i'm X" — only when X is not a
	//    transient state and looks like a name token.
	if v, ok := matchIdentity(jestemPrefixes, folded, orig); ok {
		return v, true
	}
	if v, ok := matchIdentity(imPrefixes, folded, orig); ok {
		return v, true
	}
	return "", false
}

// prefixRuneLen returns the number of runes in prefix (which is pure
// ASCII in every pattern, so this also equals byte length, but we
// count runes to stay correct against the rune-indexed orig slice).
func prefixRuneLen(prefix string) int { return len([]rune(prefix)) }

// matchPrefix returns the rendered fact for the first pattern whose
// prefix begins the folded sentence. The value is sliced from the
// original-case runes so the stored fact keeps the user's spelling.
func matchPrefix(patterns []factPattern, folded string, orig []rune) (string, bool) {
	for _, p := range patterns {
		if !strings.HasPrefix(folded, p.prefix) {
			continue
		}
		value := cleanValue(string(orig[prefixRuneLen(p.prefix):]))
		if value == "" {
			continue
		}
		return p.render(value), true
	}
	return "", false
}

// matchIdentity handles the transient-state-guarded "jestem/i'm"
// forms. It rejects multi-word values, transient states, and values
// that do not look like a name.
func matchIdentity(prefixes []string, folded string, orig []rune) (string, bool) {
	for _, prefix := range prefixes {
		if !strings.HasPrefix(folded, prefix) {
			continue
		}
		value := cleanValue(string(orig[prefixRuneLen(prefix):]))
		if value == "" {
			return "", false
		}
		if _, transient := transientStates[foldKey(value)]; transient {
			return "", false
		}
		// A name is a single token (allow a two-word "First Last").
		if words := strings.Fields(value); len(words) > 2 {
			return "", false
		}
		if !looksLikeName(value) {
			return "", false
		}
		return renderName(value), true
	}
	return "", false
}

// toLowerRune lower-cases ASCII letters; non-ASCII runes pass through
// (Polish diacritics are handled by foldRune, which operates on the
// already-lower-cased forms used in the pattern tables).
func toLowerRune(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + 32
	}
	return r
}

// cleanValue trims surrounding whitespace and trailing sentence
// punctuation, collapses inner whitespace, and enforces the length
// cap. Returns "" when nothing usable remains.
func cleanValue(v string) string {
	v = strings.TrimSpace(v)
	v = strings.Trim(v, " \t\r\n.,;:!\"'()[]{}")
	v = strings.Join(strings.Fields(v), " ")
	if v == "" {
		return ""
	}
	if len(v) > maxFactValue {
		return ""
	}
	return v
}

// looksLikeName rejects obvious non-names (digits, very short, common
// filler) so "jestem 5"/"i am ok" do not become identity facts.
func looksLikeName(v string) bool {
	if len(v) < 2 {
		return false
	}
	for _, r := range v {
		if r >= '0' && r <= '9' {
			return false
		}
	}
	return true
}

// splitSentences breaks a user message into candidate clauses on
// sentence and clause boundaries so "Cześć, lubię komputery." yields
// the declaration as its own clause. Commas split too, so a leading
// greeting ("Hi, I'm Bob") does not hide the declaration. Newlines
// also split.
func splitSentences(msg string) []string {
	repl := strings.NewReplacer(
		"\r", "\n", "!", "\n", ".", "\n", ";", "\n", ",", "\n", "\n", "\n",
	)
	parts := strings.Split(repl.Replace(msg), "\n")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// foldKey lower-cases and folds Polish diacritics so prefix matching
// is robust to "lubię" vs "lubie" and "Nazywam" vs "nazywam".
func foldKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		b.WriteRune(foldRune(r))
	}
	return b.String()
}

// foldRune maps Polish accented letters (either case) to their
// lower-case ASCII base. Other runes pass through unchanged.
func foldRune(r rune) rune {
	switch r {
	case 'ą', 'Ą':
		return 'a'
	case 'ć', 'Ć':
		return 'c'
	case 'ę', 'Ę':
		return 'e'
	case 'ł', 'Ł':
		return 'l'
	case 'ń', 'Ń':
		return 'n'
	case 'ó', 'Ó':
		return 'o'
	case 'ś', 'Ś':
		return 's'
	case 'ź', 'Ź', 'ż', 'Ż':
		return 'z'
	}
	return r
}

func renderName(v string) string {
	return fmt.Sprintf("The user's name is %s.", titleFirst(v))
}

func renderLike(v string) string {
	return fmt.Sprintf("The user likes %s.", v)
}

func renderDislike(v string) string {
	return fmt.Sprintf("The user dislikes %s.", v)
}

func renderPrefer(v string) string {
	return fmt.Sprintf("The user prefers %s.", v)
}

// titleFirst upper-cases the first rune of each whitespace-separated
// word so a captured lower-cased name reads naturally.
func titleFirst(v string) string {
	words := strings.Fields(v)
	for i, w := range words {
		r := []rune(w)
		if len(r) > 0 {
			r[0] = upperRune(r[0])
		}
		words[i] = string(r)
	}
	return strings.Join(words, " ")
}

func upperRune(r rune) rune {
	if r >= 'a' && r <= 'z' {
		return r - 32
	}
	return r
}

// SaveDeterministicUserFacts is the safety-net entry point: it runs
// the pure string-matching extractor over the given raw user-message
// texts and persists any hits to the GLOBAL store as preference
// entries, reusing saveUserFacts (which dedups against existing
// global preferences/facts). It makes NO LLM call and is safe to run
// synchronously on the exit path. Always call it after a user turn,
// regardless of what the LLM extractor returned.
func (a *AutoSaver) SaveDeterministicUserFacts(userMessages []string) {
	if a == nil || a.Global == nil {
		return
	}
	facts := ExtractUserFacts(userMessages)
	if len(facts) == 0 {
		return
	}
	a.saveUserFacts(facts)
}
