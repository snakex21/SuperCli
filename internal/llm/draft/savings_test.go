package draft

import (
	"testing"
)

func TestJaccard_Empty(t *testing.T) {
	if got := jaccard(nil, nil); got != 1.0 {
		t.Errorf("two empty sets: got %v, want 1.0", got)
	}
	a := map[string]struct{}{"x": {}}
	if got := jaccard(a, nil); got != 0.0 {
		t.Errorf("empty b: got %v, want 0.0", got)
	}
	if got := jaccard(nil, a); got != 0.0 {
		t.Errorf("empty a: got %v, want 0.0", got)
	}
}

func TestJaccard_Identical(t *testing.T) {
	a := tokenize("the quick brown fox")
	b := tokenize("the quick brown fox")
	if got := jaccard(a, b); got != 1.0 {
		t.Errorf("identical: got %v, want 1.0", got)
	}
}

func TestJaccard_Disjoint(t *testing.T) {
	a := tokenize("apple banana cherry")
	b := tokenize("dog elephant fox")
	if got := jaccard(a, b); got != 0.0 {
		t.Errorf("disjoint: got %v, want 0.0", got)
	}
}

func TestJaccard_PartialOverlap(t *testing.T) {
	a := tokenize("the quick brown fox jumps")
	b := tokenize("the lazy brown dog sleeps")
	// a: {the, quick, brown, fox, jumps} = 5
	// b: {the, lazy, brown, dog, sleeps} = 5
	// intersect: {the, brown} = 2
	// union: 5 + 5 - 2 = 8
	// jaccard = 2/8 = 0.25
	got := jaccard(a, b)
	if got < 0.24 || got > 0.26 {
		t.Errorf("partial overlap: got %v, want ~0.25", got)
	}
}

func TestTokenize_LowercasesAndStripsPunct(t *testing.T) {
	got := tokenize("Hello, World! 123")
	want := map[string]struct{}{"hello": {}, "world": {}, "123": {}}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (got %v)", len(got), len(want), got)
	}
	for k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("missing token %q (got %v)", k, got)
		}
	}
}

func TestTokenize_Deduplicates(t *testing.T) {
	got := tokenize("the the the cat")
	if len(got) != 2 {
		t.Errorf("len = %d, want 2 (the, cat)", len(got))
	}
}

func TestWordCount(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"hello", 1},
		{"hello world", 2},
		{"  spaced   out  ", 2},
		{"\n\n\t", 0},
	}
	for _, c := range cases {
		if got := wordCount(c.in); got != c.want {
			t.Errorf("wordCount(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestSavings_EmptyDraft_ZeroSavings(t *testing.T) {
	s := NewSavings()
	savings, decision := s.Add("", "verifier response", 0, 50)
	if savings != 0 {
		t.Errorf("savings = %d, want 0", savings)
	}
	if decision != "injected" {
		t.Errorf("decision = %q, want injected", decision)
	}
}

func TestSavings_Echo_HasSavings(t *testing.T) {
	s := NewSavings()
	// Identical text → jaccard = 1.0 → "used" →
	// savings = draft's output tokens (the value
	// the draft paid for the verifier).
	savings, decision := s.Add(
		"step one step two step three step four step five",
		"step one step two",
		200, 50,
	)
	if decision != "used" {
		t.Errorf("decision = %q, want used", decision)
	}
	if savings != 200 {
		t.Errorf("savings = %d, want 200 (draft's output tokens)", savings)
	}
	if s.TotalSaved() != 200 {
		t.Errorf("TotalSaved = %d, want 200", s.TotalSaved())
	}
	if s.UsedCount() != 1 {
		t.Errorf("UsedCount = %d, want 1", s.UsedCount())
	}
}

func TestSavings_Override_NoSavings(t *testing.T) {
	s := NewSavings()
	// Disjoint text → jaccard ≈ 0 → "overridden" → savings = 0.
	draft := "apple banana cherry date elderberry"
	verifier := "fox wolf bear eagle shark"
	savings, decision := s.Add(draft, verifier, 100, 200)
	if decision != "overridden" {
		t.Errorf("decision = %q, want overridden", decision)
	}
	if savings != 0 {
		t.Errorf("savings = %d, want 0 for override", savings)
	}
	if s.OverrideCount() != 1 {
		t.Errorf("OverrideCount = %d, want 1", s.OverrideCount())
	}
	if s.UsedCount() != 0 {
		t.Errorf("UsedCount = %d, want 0", s.UsedCount())
	}
}

func TestSavings_VerifierLongerThanDraft_StillCountsSavings(t *testing.T) {
	s := NewSavings()
	// The "savings" we report is the draft's output
	// tokens, regardless of how verbose the verifier
	// was. The verifier may have added a lot of
	// detail (more tokens than the draft), but the
	// draft still "paid" for the planning phase and
	// the user should see that as a saving.
	draft := "step one step two step three step four"
	verifier := "step one step two step three step four step five step six step seven step eight step nine"
	savings, decision := s.Add(draft, verifier, 50, 200)
	if decision != "used" {
		t.Errorf("decision = %q, want used (still echoed)", decision)
	}
	if savings != 50 {
		t.Errorf("savings = %d, want 50 (draft's tokens, regardless of verifier length)", savings)
	}
}

func TestSavings_FallbackToWordCount(t *testing.T) {
	s := NewSavings()
	// Token counts zero → fall back to word count
	// of the draft (savings = draft's contribution).
	draft := "one two three four five six seven eight"
	verifier := "one two three"
	savings, decision := s.Add(draft, verifier, 0, 0)
	if decision != "used" {
		t.Errorf("decision = %q, want used", decision)
	}
	// 8 words in draft.
	if savings != 8 {
		t.Errorf("savings = %d, want 8 (draft's word count)", savings)
	}
}

func TestSavings_Accumulates(t *testing.T) {
	s := NewSavings()
	s.Add("one two three four five", "one two", 5, 2)
	s.Add("alpha beta gamma delta epsilon", "alpha beta", 5, 2)
	s.Add("apple banana", "elephant tiger", 2, 2) // override
	if s.TotalSaved() != 10 {
		t.Errorf("TotalSaved = %d, want 10 (5 + 5)", s.TotalSaved())
	}
	if s.UsedCount() != 2 {
		t.Errorf("UsedCount = %d, want 2", s.UsedCount())
	}
	if s.OverrideCount() != 1 {
		t.Errorf("OverrideCount = %d, want 1", s.OverrideCount())
	}
}

func TestSavings_NilSafe(t *testing.T) {
	var s *Savings
	savings, decision := s.Add("x", "y", 10, 10)
	if savings != 0 {
		t.Errorf("nil savings should return 0, got %d", savings)
	}
	if decision != "no-recorder" {
		t.Errorf("decision = %q, want no-recorder", decision)
	}
}
