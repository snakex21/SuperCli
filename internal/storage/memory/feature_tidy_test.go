package memory

import (
	"strings"
	"testing"
	"time"
)

func TestFactTokens(t *testing.T) {
	got := factTokens("The user's name is Maks.")
	want := []string{"maks"}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("factTokens(name fact) = %v, want %v", got, want)
	}
	// Stopwords and short tokens are dropped entirely.
	if got := factTokens("the user prefers to communicate in polish"); len(got) != 2 {
		t.Fatalf("factTokens(polish fact) = %v, want 2 tokens", got)
	}
}

func TestJunkFact(t *testing.T) {
	junk := []string{
		"The user's name is not explicitly stated in the messages.",
		"The user expressed gratitude with \"dzięki to wszystko\".",
		"The user's name is unknown.",
		"The user seems to prefer concise answers.",
		"The user is likely a developer.",
	}
	for _, s := range junk {
		if !junkFact(s) {
			t.Errorf("junkFact(%q) = false, want true", s)
		}
	}
	clean := []string{
		"The user's name is Maks.",
		"The user prefers Polish for communication.",
		"The user works on a project called SuperCli built in Go.",
	}
	for _, s := range clean {
		if junkFact(s) {
			t.Errorf("junkFact(%q) = true, want false", s)
		}
	}
}

func TestSimilarFact(t *testing.T) {
	pairs := [][2]string{
		{"The user prefers to communicate in Polish.", "The user communicates primarily in Polish."},
		{"The user works on a project called SuperCli.", "The user works on a project called SuperCli built in Go."},
		{"The user's Windows username is ASRock.", "The user's Windows username is ASRock."},
	}
	for _, p := range pairs {
		if !similarFact(p[0], p[1]) {
			t.Errorf("similarFact(%q, %q) = false, want true", p[0], p[1])
		}
	}
	distinct := [][2]string{
		{"The user's name is Maks.", "The user's name is Anna."},
		{"The user works on a project called SuperCli.", "The user works on a project called NestCafe."},
		{"The user's Windows username is ASRock.", "The user's name is likely ASRock (from filesystem path)."},
	}
	for _, p := range distinct {
		if similarFact(p[0], p[1]) {
			t.Errorf("similarFact(%q, %q) = true, want false", p[0], p[1])
		}
	}
	// Cross-language near-identity must still collapse.
	if !similarFact("Użytkownik ma na imię Maks.", "The user's name is Maks.") {
		t.Errorf("similarFact(PL name, EN name) = false, want true")
	}
}

func TestDedupSimilar(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	base := time.Now().Add(-time.Hour)
	for i, content := range []string{
		"The user prefers to communicate in Polish.",
		"The user communicates primarily in Polish.", // duplicate
		"The user's name is not explicitly stated.",   // junk
		"The user's name is Maks.",
	} {
		if err := s.Put(Entry{
			ID:      "t-" + string(rune('a'+i)),
			Scope:   ScopePreference,
			Content: content,
			Source:  SourceAgent,
			// CreatedAt backdated so List ordering is deterministic
			// enough for the keep-newest rule.
		}); err != nil {
			t.Fatalf("Put: %v", err)
		}
		_ = base
	}
	removed, err := s.DedupSimilar()
	if err != nil {
		t.Fatalf("DedupSimilar: %v", err)
	}
	if removed != 2 {
		t.Fatalf("DedupSimilar removed %d, want 2 (duplicate + junk)", removed)
	}
	entries, err := s.List(ScopePreference, 100)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("after dedup %d entries remain, want 2", len(entries))
	}
	for _, e := range entries {
		if strings.Contains(e.Content, "explicitly stated") || strings.Contains(e.Content, "primarily") {
			t.Fatalf("junk/duplicate survived: %q", e.Content)
		}
	}
}
