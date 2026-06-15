package memory

import (
	"strings"
	"testing"
)

// TestExtractUserFacts is the table-driven spec for the
// deterministic (no-LLM) personal-fact extractor. It must catch
// simple Polish and English declarations, normalize the value, and
// stay conservative about transient states and questions.
func TestExtractUserFacts(t *testing.T) {
	tests := []struct {
		name string
		msgs []string
		want []string // exact rendered facts, in order
	}{
		// --- Polish names ---
		{"pl nazywam sie", []string{"Nazywam się Maks"}, []string{"The user's name is Maks."}},
		{"pl nazywam sie diacritics", []string{"nazywam sie maks"}, []string{"The user's name is Maks."}},
		{"pl mam na imie", []string{"Mam na imię Anna."}, []string{"The user's name is Anna."}},
		{"pl jestem name", []string{"Jestem Tomek"}, []string{"The user's name is Tomek."}},

		// --- English names ---
		{"en my name is", []string{"My name is Anna"}, []string{"The user's name is Anna."}},
		{"en i'm name", []string{"Hi, I'm Bob."}, []string{"The user's name is Bob."}},
		{"en i am name", []string{"I am Sarah"}, []string{"The user's name is Sarah."}},
		{"en call me", []string{"call me Max"}, []string{"The user's name is Max."}},

		// --- Polish preferences ---
		{"pl lubie", []string{"Lubię komputery"}, []string{"The user likes komputery."}},
		{"pl lubie diacritics-stripped value", []string{"lubie kawę"}, []string{"The user likes kawę."}},
		{"pl uwielbiam", []string{"Uwielbiam Go"}, []string{"The user likes Go."}},
		{"pl wole", []string{"Wolę ciemny motyw"}, []string{"The user prefers ciemny motyw."}},
		{"pl nie lubie", []string{"Nie lubię reklam"}, []string{"The user dislikes reklam."}},

		// --- English preferences ---
		{"en i like", []string{"I like computers"}, []string{"The user likes computers."}},
		{"en i love", []string{"I love Rust"}, []string{"The user likes Rust."}},
		{"en i prefer", []string{"I prefer dark mode"}, []string{"The user prefers dark mode."}},
		{"en i hate", []string{"I hate popups"}, []string{"The user dislikes popups."}},
		{"en i don't like", []string{"I don't like ads"}, []string{"The user dislikes ads."}},

		// --- Normalization ---
		{"trailing punctuation", []string{"I like coffee!!!"}, []string{"The user likes coffee."}},
		{"inner whitespace collapsed", []string{"My name is   John"}, []string{"The user's name is John."}},
		{"mixed case prefix", []string{"LUBIĘ koty"}, []string{"The user likes koty."}},
		{"sentence split", []string{"Cześć! Lubię komputery."}, []string{"The user likes komputery."}},

		// --- Exclusions: transient states after jestem / i'm ---
		{"pl jestem zmeczony", []string{"jestem zmęczony"}, nil},
		{"pl jestem w domu", []string{"jestem w domu"}, nil},
		{"en i'm tired", []string{"I'm tired"}, nil},
		{"en i'm done", []string{"I'm done"}, nil},
		{"en i am ok", []string{"I am ok"}, nil},

		// --- Exclusions: questions ---
		{"pl question", []string{"Jak masz na imię?"}, nil},
		{"en question", []string{"What's your name?"}, nil},
		{"en like question", []string{"Do you like coffee?"}, nil},

		// --- Exclusions: empty / no declaration ---
		{"empty", []string{""}, nil},
		{"unrelated", []string{"Please fix the build."}, nil},
		{"jestem multiword non-name", []string{"jestem bardzo zmęczony dzisiaj"}, nil},

		// --- Dedup within one call ---
		{"dedup same fact", []string{"I like coffee", "i like coffee"}, []string{"The user likes coffee."}},

		// --- Multiple distinct facts across messages ---
		{
			"multiple facts",
			[]string{"My name is Anna", "I like computers"},
			[]string{"The user's name is Anna.", "The user likes computers."},
		},

		// --- Value length cap (rejected when too long) ---
		{
			"too long value rejected",
			[]string{"I like " + strings.Repeat("x", 200)},
			nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractUserFacts(tc.msgs)
			if len(got) != len(tc.want) {
				t.Fatalf("ExtractUserFacts(%q) = %v, want %v", tc.msgs, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("fact[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestSaveDeterministicUserFacts checks the end-to-end safety net:
// raw user words land in the GLOBAL store as preference entries with
// no LLM call, and re-running does not create duplicates.
func TestSaveDeterministicUserFacts(t *testing.T) {
	saver, _, global := newAutoSaverForTest(t)

	saver.SaveDeterministicUserFacts([]string{"cześć, nazywam się Maks", "lubię komputery"})

	prefs, err := global.Recent(ScopePreference, 10)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(prefs) != 2 {
		t.Fatalf("global preferences = %d (%v), want 2", len(prefs), prefs)
	}
	var haveName, haveLike bool
	for _, p := range prefs {
		if strings.Contains(p.Content, "name is Maks") {
			haveName = true
		}
		if strings.Contains(p.Content, "likes komputery") {
			haveLike = true
		}
	}
	if !haveName || !haveLike {
		t.Fatalf("missing facts: name=%v like=%v (%v)", haveName, haveLike, prefs)
	}

	// Re-running with the same declarations must not duplicate.
	saver.SaveDeterministicUserFacts([]string{"nazywam się Maks", "lubię komputery"})
	prefs2, _ := global.Recent(ScopePreference, 10)
	if len(prefs2) != 2 {
		t.Fatalf("after re-run global preferences = %d (%v), want 2 (dedup)", len(prefs2), prefs2)
	}
}

// TestSaveDeterministicUserFactsTransientNoop confirms transient
// states and questions write nothing to the store.
func TestSaveDeterministicUserFactsTransientNoop(t *testing.T) {
	saver, _, global := newAutoSaverForTest(t)
	saver.SaveDeterministicUserFacts([]string{"jestem zmęczony", "What's your name?", "I'm done"})
	prefs, _ := global.Recent(ScopePreference, 10)
	if len(prefs) != 0 {
		t.Fatalf("transient states stored as preferences: %v", prefs)
	}
}
